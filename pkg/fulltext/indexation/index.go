package indexation

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/cozy/cozy-stack/client/request"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/instance"
)

type UpdateIndexNotif struct {
	InstanceName string
	DocType      string
	RetryCount   int
}

type InstanceIndex struct {
	indexList      map[string]map[string]*bleve.Index
	indexMu        *sync.Mutex
	indexHighlight bool
	indexContent   bool
}

const (
	prefixPath  = "bleve/index/"
	ContentType = "io.cozy.files.content"
)

var indexes map[string]*InstanceIndex

// Such as :
// {
// 	"cozy.tools:8080":{
// 		"io.cozy.files": {
// 			"fr": &i,
// 			"en": &i
// 		},
// 		"io.cozy.photos.albums": {
// 			"fr": &i,
// 			"en": &i
// 		},
// 		indexMu: *sync.Mutex
// 	}
// }

var instances []*instance.Instance

var languages []string

var ft_language *FastText

var updateQueue chan UpdateIndexNotif

var updateIndexRetryTimeMax = time.Minute * 10

var updateIndexRetryCountMax = 5

var updateQueueSize = 100

func StartIndex(instanceList []*instance.Instance) error {

	instances = instanceList

	ft_language = NewFastTextInst()
	ft_language.LoadModel("pkg/fulltext/indexation/lid.176.ftz")

	var err error

	languages = GetAvailableLanguages()

	indexes = make(map[string]*InstanceIndex)
	for _, inst := range instances {
		err = initializeIndexes(inst.DomainName())
		if err != nil {
			return err
		}
	}

	StartWorker()

	return UpdateAllIndexes()
}

func initializeIndexes(instName string) error {

	var err error
	indexes[instName] = &InstanceIndex{
		make(map[string]map[string]*bleve.Index),
		new(sync.Mutex),
		true,
		true,
	}

	docTypeList, err := GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	for _, docType := range docTypeList {
		err = initializeIndexDocType(instName, docType)
		if err != nil {
			return err
		}
	}

	return nil
}

func initializeIndexDocType(instName string, docType string) error {
	// Call only inside a mutex lock
	// indexes[instName] must be set

	var err error
	indexes[instName].indexList[docType] = make(map[string]*bleve.Index, len(languages))
	for _, lang := range languages {
		indexes[instName].indexList[docType][lang], err = getIndex(instName, docType, lang)
		if err != nil {
			// It failed, we remove the erroneous doctype
			deleteIndex(instName, docType, false)
			fmt.Printf("Error on getIndex:  %s\n", err)
			return err
		}
	}

	if docType == consts.Files {
		return initializeIndexDocType(instName, ContentType)
	}

	return nil
}

func findWhichLangIndexDoc(indexList map[string]*bleve.Index, id string) string {
	for lang := range indexList {
		doc, _ := (*indexList[lang]).Document(id)
		if doc != nil {
			return lang
		}

	}
	return ""
}

func getIndex(instName string, docType string, lang string) (*bleve.Index, error) {
	// call only inside a mutex

	// Send fetched index if already exists
	if indexes[instName].indexList[docType][lang] != nil {
		fmt.Println("Fetch loaded index")
		return indexes[instName].indexList[docType][lang], nil
	}

	fullIndexPath := prefixPath + instName + "/" + lang + "/" + docType

	i, errOpen := bleve.Open(fullIndexPath)

	// Create it if it doesn't exist
	if errOpen == bleve.ErrorIndexPathDoesNotExist {
		indexMapping := bleve.NewIndexMapping()
		err := AddTypeMapping(indexMapping, docType, lang, indexes[instName].indexHighlight)
		if err != nil {
			fmt.Printf("Error on adding type mapping: %s\n", err)
			return nil, err
		}
		fmt.Printf("Creating new index %s...\n", fullIndexPath)
		i, err2 := bleve.New(fullIndexPath, indexMapping)
		if err2 != nil {
			fmt.Printf("Error on creating new Index: %s\n", err2)
			return nil, err2
		}

		// Set the couchdb seq to 0 (default) when creating an index (to fetch all changes on UpdateIndex())
		err = setStoreSeq(&i, "0")
		if err != nil {
			fmt.Printf("Error on SetStoreSeq: %s\n", err)
			return nil, err
		}

		return &i, nil

	} else if errOpen != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", fullIndexPath, errOpen)
		return nil, errOpen
	}

	fmt.Println("found existing Index", instName, docType, lang)
	return &i, nil
}

func UpdateAllIndexes() error {

	docTypeList, err := GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	for instName := range indexes {
		for _, docType := range docTypeList {
			err := AddUpdateIndexJob(UpdateIndexNotif{instName, docType, 0})
			if err != nil {
				fmt.Printf("Could not add update index job: %s\n", err)
			}
		}
	}
	return nil
}

func UpdateIndex(instName string, docType string) error {

	err := makeSureInstanceReady(instName)
	if err != nil {
		fmt.Printf("Error on makeSureInstanceReady: %s\n", err)
		return err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	err = makeSureInstanceDocTypeReady(instName, docType)
	if err != nil {
		fmt.Printf("Error on makeSureInstanceDocTypeReady: %s\n", err)
		return err
	}

	// Set request to get last changes
	last_store_seq, err := getStoreSeq(indexes[instName].indexList[docType][languages[0]])
	if err != nil {
		fmt.Printf("Error on GetStoredSeq: %s\n", err)
		return err
	}

	var request = &couchdb.ChangesRequest{
		DocType:     docType,
		Since:       last_store_seq, // Set with last seq
		IncludeDocs: true,
	}

	inst, err := instance.Get(instName)
	if err != nil {
		fmt.Printf("Error on getting instance from instance name: %s\n", err)
		return err
	}

	// Fetch last changes
	// TODO : check how getchanges behave when there are multiple changes for a doc since last seq
	response, err := couchdb.GetChanges(inst, request)
	if err != nil {
		fmt.Printf("Error on getChanges: %s\n", err)
		return err
	}

	// Index thoses last changes
	batch := make(map[string]*bleve.Batch, len(languages))
	for lang := range indexes[instName].indexList[docType] {
		batch[lang] = (*indexes[instName].indexList[docType][lang]).NewBatch()
	}

	for i, result := range response.Results {

		if typ, ok := result.Doc.M["type"]; !ok || typ.(string) == "directory" {
			// This is a directory, we ignore this doc
			continue
		}

		originalIndexLang := findWhichLangIndexDoc(indexes[instName].indexList[docType], result.DocID)

		// Delete the files that are trashed = true or _deleted = true
		if result.Doc.Get("_deleted") == true || result.Doc.Get("trashed") == true {

			err := DeleteDoc(instName, docType, originalIndexLang, result.DocID)
			if err != nil {
				fmt.Printf("Error on DeleteDoc: %s\n", err)
				return err
			}
			continue
		}

		if originalIndexLang != "" {
			// We found the document so we should update it in the original index

			err := UpdateDoc(batch, instName, docType, originalIndexLang, result)
			if err != nil {
				fmt.Printf("Error on UpdateDoc: %s\n", err)
				return err
			}

		} else {
			// We couldn't find the document, so we should create it in the predicted language index

			err := CreateDoc(batch, instName, docType, result)
			if err != nil {
				fmt.Printf("Error on CreateDoc: %s\n", err)
				return err
			}
		}

		// Batch files
		if i%300 == 0 {
			for lang := range indexes[instName].indexList[docType] {
				(*indexes[instName].indexList[docType][lang]).Batch(batch[lang])
				batch[lang] = (*indexes[instName].indexList[docType][lang]).NewBatch()
			}
		}

	}

	for lang := range indexes[instName].indexList[docType] {
		(*indexes[instName].indexList[docType][lang]).Batch(batch[lang])

		// Store the new seq number in the indexes
		err = setStoreSeq(indexes[instName].indexList[docType][lang], response.LastSeq)
		if err != nil {
			fmt.Printf("Error on SetStoreSeq: %s\n", err)
			return err
		}
	}

	return nil
}

func DeleteDoc(instName string, docType string, originalIndexLang string, DocID string) error {
	if originalIndexLang != "" {
		err := (*indexes[instName].indexList[docType][originalIndexLang]).Delete(DocID)
		if err != nil {
			return err
		}
		if indexes[instName].indexContent && docType == consts.Files {
			return (*indexes[instName].indexList[ContentType][originalIndexLang]).Delete(DocID)
		}
	}
	// else the file has already been deleted or hadn't had been indexed before either
	return nil
}

func UpdateDoc(batch map[string]*bleve.Batch, instName string, docType string, originalIndexLang string, result couchdb.Change) error {
	result.Doc.M["docType"] = docType

	// If doc is a file and indexContent is true, we need to check if content has been modified since last indexation
	if indexes[instName].indexContent && docType == consts.Files {
		md5sum, err := getStoreMd5sum(indexes[instName].indexList[docType][originalIndexLang], result.DocID)
		if err != nil {
			fmt.Printf("Error on getStoreMd5sum: %s\n", err)
			return err
		}
		if md5sum != result.Doc.M["md5sum"] {
			// Get new content and index it
			content, err := getContentFile(instName, result.DocID)
			if err != nil {
				fmt.Printf("Error on getContentFile: %s\n", err)
				return err
			}
			err = updateContent(instName, originalIndexLang, result.DocID, content)
			if err != nil {
				fmt.Printf("Error on UpdateContent: %s\n", err)
				return err
			}

			err = setStoreMd5sum(indexes[instName].indexList[docType][originalIndexLang], result.DocID, result.Doc.M["md5sum"].(string))
			if err != nil {
				fmt.Printf("Error on setStoreMd5sum: %s\n", err)
				return err
			}
		}
	}

	return batch[originalIndexLang].Index(result.DocID, result.Doc.M)
}

func updateContent(instName string, originalIndexLang string, docID string, content string) error {

	return (*indexes[instName].indexList[ContentType][originalIndexLang]).Index(docID, map[string]string{"content": content, "docType": consts.Files})
}

func CreateDoc(batch map[string]*bleve.Batch, instName string, docType string, result couchdb.Change) error {

	var pred string
	var err error

	// If doc is a file and indexContent is true, we need to index content
	if indexes[instName].indexContent && docType == consts.Files {
		content, err := getContentFile(instName, result.DocID)
		if err != nil {
			fmt.Printf("Error on getContentFile", err)
			return err
		}

		pred, err = ft_language.GetLanguage(content)
		if err != nil {
			fmt.Printf("Error on language prediction:  %s\n", err)
			return err
		}

		err = updateContent(instName, pred, result.DocID, content)
		if err != nil {
			fmt.Printf("Error on UpdateContent: %s\n", err)
			return err
		}

		err = setStoreMd5sum(indexes[instName].indexList[docType][pred], result.DocID, result.Doc.M["md5sum"].(string))
		if err != nil {
			fmt.Printf("Error on setStoreMd5sum: %s\n", err)
			return err
		}
	} else {
		pred, err = ft_language.GetLanguage(result.Doc.M["name"].(string))
		if err != nil {
			fmt.Printf("Error on language prediction:  %s\n", err)
			return err
		}
	}

	result.Doc.M["docType"] = docType

	return batch[pred].Index(result.DocID, result.Doc.M)
}

func ReIndex(instName string, docType string) error {

	err := makeSureInstanceReady(instName)
	if err != nil {
		return err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	err = makeSureInstanceDocTypeReady(instName, docType)
	if err != nil {
		return err
	}
	deleteIndex(instName, docType, false)

	// Re-initialize indexes var with docType
	err = initializeIndexDocType(instName, docType)
	if err != nil {
		return err
	}

	// Update index
	err = AddUpdateIndexJob(UpdateIndexNotif{instName, docType, 0})
	if err != nil {
		return err
	}

	return nil
}

func ReIndexAll(instName string) error {

	docTypeList, err := GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	for _, docType := range docTypeList {
		err := ReIndex(instName, docType)
		if err != nil {
			return err
		}
	}

	return nil
}

func ReplicateAll(instName string) error {

	err := checkInstance(instName)
	if err != nil {
		return err
	}

	for docType := range indexes[instName].indexList {
		for lang := range indexes[instName].indexList[docType] {
			_, err := Replicate(instName, docType, lang)
			if err != nil {
				fmt.Printf("Error on replication: %s\n", err)
				return err
			}
		}
	}

	return nil
}

func Replicate(instName string, docType string, lang string) (string, error) {

	err := checkInstance(instName)
	if err != nil {
		return "", err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	err = checkInstanceDocType(instName, docType)
	if err != nil {
		return "", err
	}

	path := prefixPath + instName + "/" + lang + "/" + docType

	_, store, err := (*indexes[instName].indexList[docType][lang]).Advanced()
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	r, err := store.Reader()
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	tmpFile, err := ioutil.TempFile(path, "store.replicate.")
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	err = r.WriteTo(tmpFile)
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	err = tmpFile.Close()
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	err = r.Close()
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	return tmpFile.Name(), nil
}

func sendIndexToQuery(instName string, docType string, lang string) error {

	tmpFileName, err := Replicate(instName, docType, lang)
	if err != nil {
		fmt.Println("Error on replicate when sending index to query")
		fmt.Println(err)
		return err
	}
	defer os.Remove(tmpFileName)

	body, err := ioutil.ReadFile(tmpFileName)
	if err != nil {
		fmt.Println("Error opening new alias")
		fmt.Println(err)
		return err
	}

	inst, err := instance.Get(instName)
	if err != nil {
		fmt.Printf("Error on getting instance from instance name: %s\n", err)
		return err
	}

	opts := &request.Options{
		Method: http.MethodPost,
		Scheme: inst.Scheme(),
		Domain: inst.DomainName(),
		Path:   "/fulltext/_update_index_alias/" + instName + "/" + docType + "/" + lang,
		Headers: request.Headers{
			"Content-Type": "application/indexstore", // See which content-type ?
			// Deal with permissions
		},
		Body: bytes.NewReader(body),
	}
	_, err = request.Req(opts)
	if err != nil {
		fmt.Println("Error on POST request")
		fmt.Println(err)
		return err
	}
	return nil
}

func setStoreSeq(index *bleve.Index, rev string) error {
	// Call only inside a mutex lock
	return (*index).SetInternal([]byte("seq"), []byte(rev))
}

func getStoreSeq(index *bleve.Index) (string, error) {
	// Call only inside a mutex lock
	res, err := (*index).GetInternal([]byte("seq"))
	return string(res), err
}

func setStoreMd5sum(index *bleve.Index, fileId string, md5sum string) error {
	// Call only inside a mutex lock
	return (*index).SetInternal([]byte("md5sum"+fileId), []byte(md5sum))
}

func getStoreMd5sum(index *bleve.Index, fileId string) (string, error) {
	// Call only inside a mutex lock
	res, err := (*index).GetInternal([]byte("md5sum" + fileId))
	return string(res), err
}

func getExistingContent(index *bleve.Index, id string) (string, error) {
	// Call only inside a mutex lock
	doc, err := (*index).Document(id)
	if err != nil {
		return "", err
	}
	for _, docIndex := range doc.Fields {
		if docIndex.Name() == "content" {
			return string(docIndex.Value()[:docIndex.NumPlainTextBytes()]), nil
		}
	}
	return "", errors.New("No existing content found in the index.")
}

func getContentFile(uuid string, id string) (string, error) {
	// This is a mock function
	return "", nil
}

func DeleteAllIndexesInstance(instName string, querySide bool) error {

	err := checkInstance(instName)
	if err != nil {
		return err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	for docType := range indexes[instName].indexList {
		if docType == ContentType {
			continue
		}
		err := deleteIndex(instName, docType, querySide)
		if err != nil {
			return err
		}
	}

	delete(indexes, instName)
	return os.RemoveAll(prefixPath + instName)
}

func DeleteIndexLock(instName string, docType string, querySide bool) error {
	err := checkInstance(instName)
	if err != nil {
		return err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	err = checkInstanceDocType(instName, docType)
	if err != nil {
		return err
	}

	return deleteIndex(instName, docType, querySide)
}

func deleteIndex(instName string, docType string, querySide bool) error {
	// Call only inside a mutex lock

	for lang := range indexes[instName].indexList[docType] {
		if indexes[instName].indexList[docType][lang] != nil {
			(*indexes[instName].indexList[docType][lang]).Close()
		}
		err := os.RemoveAll(prefixPath + instName + "/" + lang + "/" + docType)
		if err != nil {
			return err
		}
		if querySide {
			err = notifyDeleteIndexQuery(instName, docType, lang)
			if err != nil {
				fmt.Printf("Error telling query to delete index: %s\n", err)
				return err
			}
		}
	}

	delete(indexes[instName].indexList, docType)

	if docType == consts.Files {
		return deleteIndex(instName, ContentType, querySide)
	}

	return nil
}

func notifyDeleteIndexQuery(instName string, docType string, lang string) error {

	inst, err := instance.Get(instName)
	if err != nil {
		fmt.Printf("Error on getting instance from instance name: %s\n", err)
		return err
	}

	opts := &request.Options{
		Method:  http.MethodPost,
		Scheme:  inst.Scheme(),
		Domain:  inst.DomainName(),
		Path:    "/fulltext/_delete_index_query/" + instName + "/" + docType + "/" + lang,
		Headers: request.Headers{
			// Deal with permissions
		},
		Body: nil,
	}
	_, err = request.Req(opts)
	if err != nil {
		fmt.Println("Error on POST request")
		fmt.Println(err)
		return err
	}

	return nil
}

func makeSureInstanceReady(instName string) error {
	err := checkInstance(instName)
	if err != nil {
		// Not existing already, try to initialize it (for mutex)
		err = initializeIndexes(instName)
		if err != nil {
			return err
		}
	}
	return nil
}

func makeSureInstanceDocTypeReady(instName string, docType string) error {
	// Call only inside a mutex lock
	// indexes[instName] must be set

	err := checkInstanceDocType(instName, docType)
	if err != nil {
		err = initializeIndexDocType(instName, docType)
		if err != nil {
			return err
		}
	}
	return nil
}

func checkInstance(instName string) error {
	if _, ok := indexes[instName]; !ok {
		return errors.New("Instance not found in CheckInstance")
	}
	return nil
}

func checkInstanceDocType(instName string, docType string) error {
	if _, ok := indexes[instName].indexList[docType]; !ok {
		return errors.New("DocType not found in CheckInstanceDocType")
	}
	return nil
}

func SetOptionInstance(instName string, options map[string]bool) (map[string]bool, error) {
	err := makeSureInstanceReady(instName)
	if err != nil {
		return nil, err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	if content, ok := options["content"]; ok {
		indexes[instName].indexContent = content
	}

	if highlight, ok := options["highlight"]; ok {
		indexes[instName].indexHighlight = highlight
	}

	return map[string]bool{
		"content":  indexes[instName].indexContent,
		"higlight": indexes[instName].indexHighlight,
	}, nil
}

func StartWorker() {

	updateQueue = make(chan UpdateIndexNotif, updateQueueSize)

	go func(updateQueue <-chan UpdateIndexNotif) {
		for notif := range updateQueue {
			err := UpdateIndex(notif.InstanceName, notif.DocType)
			if err != nil {
				fmt.Printf("Error on UpdateIndex: %s\n", err)
				// We retry the indexation after an indexUpdateRetryTime
				go func(updateNotif UpdateIndexNotif) {
					timer := time.NewTimer(updateIndexRetryTimeMax)
					<-timer.C
					updateNotif.RetryCount += 1
					err := AddUpdateIndexJob(updateNotif)
					if err != nil {
						fmt.Printf("Error on AddUpdateIndexJob: %s\n", err)
					}
				}(notif)
			} else {
				// Send the new index to the search side
				for lang := range indexes[notif.InstanceName].indexList[notif.DocType] {
					err := sendIndexToQuery(notif.InstanceName, notif.DocType, lang) // TODO: deal with errors
					if err != nil {
						fmt.Printf("Error on sendIndexToQuery: %s\n", err)
					}
					if notif.DocType == consts.Files {
						// Also send content to query side
						err := sendIndexToQuery(notif.InstanceName, ContentType, lang)
						if err != nil {
							fmt.Printf("Error on sendIndexToQuery: %s\n", err)
						}
					}
				}
			}
		}
	}(updateQueue)
}

func AddUpdateIndexJob(updateNotif UpdateIndexNotif) error {

	if updateNotif.RetryCount > updateIndexRetryCountMax {
		return errors.New("RetryCount has exceeded updateIndexRetryCountMax for " + updateNotif.DocType + "doctype")
	}

	select {
	case updateQueue <- updateNotif:
		return nil
	default:
		return errors.New("Update Queue is full, can't add new doctype to the update queue for now")
	}
}

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
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/instance"
)

type file struct {
	ID         string        `json:"_id"`
	Rev        string        `json:"_rev"`
	Type       string        `json:"type"`
	Name       string        `json:"name"`
	DirID      string        `json:"dir_id"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Size       string        `json:"size"`
	Md5Sum     string        `json:"md5sum"`
	Mime       string        `json:"mime"`
	Class      string        `json:"class"`
	Executable bool          `json:"executable"`
	Trashed    bool          `json:"trashed"` //TODO: pay attention to trash or not
	Tags       []interface{} `json:"tags"`
	DocType    string        `json:"docType"`
	Metadata   struct {
		Datetime         time.Time `json:"datetime"`
		ExtractorVersion int       `json:"extractor_version"`
		Height           int       `json:"height"`
		Width            int       `json:"width"`
	} `json:"metadata"`
}

type photoAlbum struct {
	ID        string    `json:"_id"`
	Rev       string    `json:"_rev"`
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	DocType   string    `json:"docType"`
}

type updateIndexNotif struct {
	InstanceName string
	DocType      string
}

type InstanceIndex struct {
	indexList map[string]map[string]*bleve.Index
	indexMu   *sync.Mutex
}

const (
	prefixPath = "bleve/index/"
)

var indexes map[string]InstanceIndex

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

var docTypeList []string

var instances []*instance.Instance

var languages []string

var ft_language *FastText

var updateQueue chan updateIndexNotif

func StartIndex(instanceList []*instance.Instance, docTypeListInitialize []string) error {

	instances = instanceList

	ft_language = NewFastTextInst()
	ft_language.LoadModel("pkg/fulltext/indexation/lid.176.ftz")

	var err error

	languages = GetAvailableLanguages()

	docTypeList = docTypeListInitialize

	indexes = make(map[string]InstanceIndex)
	for _, inst := range instances {
		err = initializeIndexes(inst.DomainName())
		if err != nil {
			return err
		}
	}

	return AllIndexesUpdate()
}

func initializeIndexes(instName string) error {

	var err error
	indexes[instName] = InstanceIndex{
		make(map[string]map[string]*bleve.Index, len(docTypeList)),
		new(sync.Mutex),
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
		err := AddTypeMapping(indexMapping, docType, lang)
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

		// Set the couchdb seq to 0 (default) when creating an index (to fetch all changes on IndexUpdate())
		setStoreSeq(&i, "0")

		return &i, nil

	} else if errOpen != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", fullIndexPath, errOpen)
		return nil, errOpen
	}

	fmt.Println("found existing Index", instName, docType, lang)
	return &i, nil
}

func AllIndexesUpdate() error {
	for instName := range indexes {
		for docType := range indexes[instName].indexList {
			err := AddUpdateIndexJob(instName, docType)
			if err != nil {
				fmt.Printf("Could not add update index job: %s\n", err)
			}
		}
	}
	return nil
}

func IndexUpdate(instName string, docType string) error {

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

		originalIndexLang := findWhichLangIndexDoc(indexes[instName].indexList[docType], result.DocID)

		// Delete the files that are trashed = true or _deleted = true
		if result.Doc.Get("_deleted") == true || result.Doc.Get("trashed") == true {
			if originalIndexLang == "" {
				// The file has already been deleted or hadn't had been indexed before either
				continue
			}
			(*indexes[instName].indexList[docType][originalIndexLang]).Delete(result.DocID)
			continue
		}

		if _, ok := result.Doc.M["name"]; !ok {
			// TODO : find out out why some changes don't correspond to files only and thus don't have "name" field
			fmt.Printf("Error on fetching name\n")
			continue
		}

		if originalIndexLang != "" {
			// We found the document so we should update it the original index
			result.Doc.M["docType"] = docType
			batch[originalIndexLang].Index(result.DocID, result.Doc.M)
		} else {
			// We couldn't find the document, so we predict the language to index it in the right index
			pred, err := ft_language.GetLanguage(result.Doc.M["name"].(string)) // TODO: predict on content and not "name" field in particular
			if err != nil {
				fmt.Printf("Error on language prediction:  %s\n", err)
				continue
			}
			result.Doc.M["docType"] = docType
			batch[pred].Index(result.DocID, result.Doc.M)
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
		setStoreSeq(indexes[instName].indexList[docType][lang], response.LastSeq)
	}

	return nil
}

func ReIndex(instName string, docType string) error {

	err := checkInstance(instName)
	if err != nil {
		// Not existing already, try to initialize it (for mutex)
		err = initializeIndexes(instName)
		if err != nil {
			return err
		}
	} else {
		// Save indexes before reindexing
		err = ReplicateAll(instName)
		if err != nil {
			return err
		}
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	err = checkInstanceDocType(instName, docType)
	if err == nil {
		// Already exists, need to remove
		deleteIndex(instName, docType, false)
	}

	// Re-initialize indexes var with docType
	err = initializeIndexDocType(instName, docType)
	if err != nil {
		return err
	}
	// Add it to docTypeList if not already
	addNewDoctypeToDocTypeList(docType)

	// Update index
	err = AddUpdateIndexJob(instName, docType)
	if err != nil {
		return err
	}

	return nil
}

func ReIndexAll(instName string) error {

	for _, docType := range docTypeList {
		err := ReIndex(instName, docType)
		if err != nil {
			return err
		}
	}

	return nil
}

func addNewDoctypeToDocTypeList(newDocType string) {
	for _, docType := range docTypeList {
		if docType == newDocType {
			return
		}
	}

	// newDocType not found
	docTypeList = append(docTypeList, newDocType)
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

func setStoreSeq(index *bleve.Index, rev string) {
	// Call only inside a mutex lock
	(*index).SetInternal([]byte("seq"), []byte(rev))
}

func getStoreSeq(index *bleve.Index) (string, error) {
	// Call only inside a mutex lock
	res, err := (*index).GetInternal([]byte("seq"))
	return string(res), err
}

func DeleteAllIndexesInstance(instName string, querySide bool) error {

	err := checkInstance(instName)
	if err != nil {
		return err
	}

	indexes[instName].indexMu.Lock()
	defer indexes[instName].indexMu.Unlock()

	for docType := range indexes[instName].indexList {
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

func StartWorker() {

	updateQueue = make(chan updateIndexNotif, 10)

	go func(updateQueue <-chan updateIndexNotif) {
		for notif := range updateQueue {
			IndexUpdate(notif.InstanceName, notif.DocType) // TODO: deal with errors
			// Send the new index to the search side
			for lang := range indexes[notif.InstanceName].indexList[notif.DocType] {
				sendIndexToQuery(notif.InstanceName, notif.DocType, lang) // TODO: deal with errors
			}
		}
	}(updateQueue)
}

func AddUpdateIndexJob(instName string, docType string) error {
	select {
	case updateQueue <- updateIndexNotif{instName, docType}:
		return nil
	default:
		return errors.New("Update Queue is full, can't add new doctype to the update queue for now (docTypes before " + docType + " were correctly added to update queue).")
	}
}

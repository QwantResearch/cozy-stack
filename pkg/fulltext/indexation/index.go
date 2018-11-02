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

type documentIndexes map[string]map[string]*bleve.Index

// Such as :
// {
// 	"io.cozy.files": {
// 		"fr": &i,
// 		"en": &i
// 	},
// 	"io.cozy.photos.albums": {
// 		"fr": &i,
// 		"en": &i
// 	}
// }

const (
	prefixPath = "bleve/index/"
)

var indexes documentIndexes

var docTypeList []string

var inst *instance.Instance

var languages []string

var ft_language *FastText

var updateQueue chan string

var indexMu *sync.Mutex

func StartIndex(instance *instance.Instance) error {

	inst = instance

	ft_language = NewFastTextInst()
	ft_language.LoadModel("pkg/fulltext/indexation/lid.176.ftz")

	indexMu = new(sync.Mutex)

	var err error

	languages = GetAvailableLanguages()

	docTypeList, err = GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	err = InitializeIndexes()
	if err != nil {
		return err
	}

	return AllIndexesUpdate()
}

func InitializeIndexes() error {

	var err error

	indexes = make(map[string]map[string]*bleve.Index, len(docTypeList))
	for _, docType := range docTypeList {
		indexes[docType] = make(map[string]*bleve.Index, len(languages))
		for _, lang := range languages {
			indexes[docType][lang], err = GetIndex(docType, lang)
			if err != nil {
				fmt.Printf("Error on GetIndex:  %s\n", err)
				return err
			}
		}
	}

	return nil
}

func FindWhichLangIndexDoc(indexList map[string]*bleve.Index, id string) string {
	for lang := range indexList {
		doc, _ := (*indexList[lang]).Document(id)
		if doc != nil {
			return lang
		}

	}
	return ""
}

func GetIndex(docType string, lang string) (*bleve.Index, error) {

	// Send fetched index if already exists
	if indexes[docType][lang] != nil {
		fmt.Println("Fetch loaded index")
		return indexes[docType][lang], nil
	}

	fullIndexPath := prefixPath + lang + "/" + docType

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
		SetStoreSeq(&i, "0")

		return &i, nil

	} else if errOpen != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", fullIndexPath, errOpen)
		return nil, errOpen
	}

	fmt.Println("found existing Index", docType, lang)
	return &i, nil
}

func AllIndexesUpdate() error {
	for docType := range indexes {
		err := AddUpdateIndexJob(docType)
		if err != nil {
			fmt.Printf("Could not add update index job: %s\n", err)
		}
	}
	return nil
}

func IndexUpdate(docType string) error {

	indexMu.Lock()
	defer indexMu.Unlock()

	// Set request to get last changes
	last_store_seq, err := GetStoreSeq(indexes[docType][languages[0]])
	if err != nil {
		fmt.Printf("Error on GetStoredSeq: %s\n", err)
	}

	var request = &couchdb.ChangesRequest{
		DocType:     docType,
		Since:       last_store_seq, // Set with last seq
		IncludeDocs: true,
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
	for lang := range indexes[docType] {
		batch[lang] = (*indexes[docType][lang]).NewBatch()
	}

	for i, result := range response.Results {

		originalIndexLang := FindWhichLangIndexDoc(indexes[docType], result.DocID)

		// Delete the files that are trashed = true or _deleted = true
		if result.Doc.Get("_deleted") == true || result.Doc.Get("trashed") == true {
			if originalIndexLang == "" {
				// The file has already been deleted or hadn't had been indexed before either
				continue
			}
			(*indexes[docType][originalIndexLang]).Delete(result.DocID)
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
			for lang := range indexes[docType] {
				(*indexes[docType][lang]).Batch(batch[lang])
				batch[lang] = (*indexes[docType][lang]).NewBatch()
			}
		}

	}

	for lang := range indexes[docType] {
		(*indexes[docType][lang]).Batch(batch[lang])

		// Store the new seq number in the indexes
		SetStoreSeq(indexes[docType][lang], response.LastSeq)
	}

	return nil
}

func ReIndex() error {

	// Save indexes before reindexing
	err := ReplicateAll()
	if err != nil {
		return err
	}

	indexMu.Lock()
	defer indexMu.Unlock()

	// Close indexes
	for docType := range indexes {
		for lang := range indexes[docType] {
			if indexes[docType][lang] != nil {
				err = (*indexes[docType][lang]).Close()
				if err != nil {
					return err
				}
			}
		}
	}

	// Remove indexes
	err = os.RemoveAll(prefixPath)
	if err != nil {
		return err
	}

	// Fetch docTypeList from the mapping file, in case it changed
	docTypeList, err = GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	// Re-initialize indexes var with new docTypeList
	err = InitializeIndexes()
	if err != nil {
		return err
	}

	// Update all indexes
	for docType, _ := range indexes {
		AddUpdateIndexJob(docType)
		if err != nil {
			return err
		}
	}

	return nil

}

func ReplicateAll() error {

	for docType := range indexes {
		for lang := range indexes[docType] {
			_, err := Replicate(indexes[docType][lang], prefixPath+lang+"/"+docType)
			if err != nil {
				fmt.Printf("Error on replication: %s\n", err)
				return err
			}
		}
	}

	return nil
}

func Replicate(index *bleve.Index, path string) (string, error) {

	indexMu.Lock()
	defer indexMu.Unlock()

	_, store, err := (*index).Advanced()
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

func SendIndexToQuery(docType string, lang string) error {

	tmpFileName, err := Replicate(indexes[docType][lang], prefixPath+lang+"/"+docType)
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

	opts := &request.Options{
		Method: http.MethodPost,
		Scheme: inst.Scheme(),
		Domain: inst.DomainName(),
		Path:   "/fulltext/_update_index_alias/" + docType + "/" + lang,
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

func SetStoreSeq(index *bleve.Index, rev string) {
	(*index).SetInternal([]byte("seq"), []byte(rev))
}

func GetStoreSeq(index *bleve.Index) (string, error) {
	res, err := (*index).GetInternal([]byte("seq"))
	return string(res), err
}

func StartWorker() {

	updateQueue = make(chan string, 10)

	go func(updateQueue <-chan string) {
		for docType := range updateQueue {
			IndexUpdate(docType) // TODO: deal with errors
			// Send the new index to the search side
			for lang := range indexes[docType] {
				SendIndexToQuery(docType, lang) // TODO: deal with errors
			}
		}
	}(updateQueue)
}

func AddUpdateIndexJob(docType string) error {
	select {
	case updateQueue <- docType:
		return nil
	default:
		return errors.New("Update Queue is full, can't add new doctype to the update queue for now (docTypes before " + docType + " were correctly added to update queue).")
	}
}

package indexation

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/blevesearch/bleve"
	// "github.com/blevesearch/bleve/mapping"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/index/search"
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

// documentIndexes structure that encapsulates, the doctype, the index path and the different language indexes corresponding to this doctype
type documentIndexes struct {
	docType   string
	indexPath string
	indexList map[string]*bleve.Index // The mapping between the languages and the corresponding indexes
	updateMu  *sync.Mutex
}

var indexes []documentIndexes

var inst *instance.Instance

var languages []string

var prefixPath string

var ft_language *FastText

func StartIndex(instance *instance.Instance) error {

	inst = instance

	indexes = []documentIndexes{
		documentIndexes{
			docType:   consts.PhotosAlbums,
			indexPath: "photo.albums.bleve",
			indexList: map[string]*bleve.Index{
				"fr": nil,
				"en": nil,
			},
			updateMu: new(sync.Mutex),
		},
		documentIndexes{
			docType:   consts.Files,
			indexPath: "file.bleve",
			indexList: map[string]*bleve.Index{
				"fr": nil,
				"en": nil,
			},
			updateMu: new(sync.Mutex),
		},
		documentIndexes{
			docType:   "io.cozy.bank.accounts", // TODO : check why it doesn't exist in consts
			indexPath: "bank.accounts.bleve",
			indexList: map[string]*bleve.Index{
				"fr": nil,
				"en": nil,
			},
			updateMu: new(sync.Mutex),
		},
	}

	ft_language = NewFastTextInst()
	ft_language.LoadModel("pkg/index/indexation/lid.176.ftz")

	var err error

	languages = GetAvailableLanguages()

	prefixPath = "bleve/index/"

	for _, lang := range languages {
		for _, docIndexes := range indexes {
			docIndexes.indexList[lang], err = GetIndex(docIndexes.indexPath, lang, docIndexes.docType)
			if err != nil {
				return err
			}
		}
	}

	AllIndexesUpdate()

	return nil
}

func FindWhichLangIndexDoc(indexList map[string]*bleve.Index, id string) string {
	for _, lang := range languages {
		doc, _ := (*indexList[lang]).Document(id)
		if doc != nil {
			return lang
		}

	}
	return ""
}

func GetIndex(indexPath string, lang string, docType string) (*bleve.Index, error) {
	indexMapping := bleve.NewIndexMapping()
	AddTypeMapping(indexMapping, docType, lang)

	fullIndexPath := prefixPath + lang + "/" + indexPath

	i, err1 := bleve.Open(fullIndexPath)

	// Create it if it doesn't exist
	if err1 == bleve.ErrorIndexPathDoesNotExist {
		fmt.Printf("Creating new index %s...\n", fullIndexPath)
		i, err2 := bleve.New(fullIndexPath, indexMapping)
		if err2 != nil {
			fmt.Printf("Error on creating new Index: %s\n", err2)
			return &i, err2
		}

		// Set the couchdb seq to 0 (default) when creating an index (to fetch all changes on IndexUpdate())
		SetStoreSeq(&i, "0")

		return &i, nil

	} else if err1 != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", fullIndexPath, err1)
		return &i, err1
	}

	fmt.Println("found existing Index", indexPath, lang)
	return &i, nil
}

func AllIndexesUpdate() error {
	for _, docIndexes := range indexes {
		err := IndexUpdate(docIndexes)
		if err != nil {
			continue
			// return err // TODO : change behaviour so that we don't ignore this error
		}
		fmt.Println(docIndexes.indexPath, "updated")
	}
	return nil
}

func IndexUpdate(docIndexes documentIndexes) error {

	docIndexes.updateMu.Lock()
	defer docIndexes.updateMu.Unlock()

	// Set request to get last changes
	last_store_seq, err := GetStoreSeq(docIndexes.indexList["en"])
	if err != nil {
		fmt.Printf("Error on GetStoredSeq: %s\n", err)
	}

	var request = &couchdb.ChangesRequest{
		DocType:     docIndexes.docType,
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
	for _, lang := range languages {
		batch[lang] = (*docIndexes.indexList[lang]).NewBatch()
	}

	for i, result := range response.Results {

		originalIndexLang := FindWhichLangIndexDoc(docIndexes.indexList, result.DocID)

		// Delete the files that are trashed = true or _deleted = true
		if result.Doc.Get("_deleted") == true || result.Doc.Get("trashed") == true {
			if originalIndexLang == "" {
				// The file has already been deleted or hadn't had been indexed before either
				continue
			}
			(*docIndexes.indexList[originalIndexLang]).Delete(result.DocID)
			continue
		}

		if _, ok := result.Doc.M["name"]; !ok {
			// TODO : find out out why some changes don't correspond to files only and thus don't have "name" field
			fmt.Printf("Error on fetching name\n")
			continue
		}

		if originalIndexLang != "" {
			// We found the document so we should update it the original index
			result.Doc.M["docType"] = docIndexes.docType
			batch[originalIndexLang].Index(result.DocID, result.Doc.M)
		} else {
			// We couldn't find the document, so we predict the language to index it in the right index
			pred, err := ft_language.GetLanguage(result.Doc.M["name"].(string)) // TODO: predict on content and not "name" field in particular
			if err != nil {
				fmt.Printf("Error on language prediction:  %s\n", err)
				continue
			}
			result.Doc.M["docType"] = docIndexes.docType
			batch[pred].Index(result.DocID, result.Doc.M)
		}

		// Batch files
		if i%300 == 0 {
			for _, lang := range languages {
				(*docIndexes.indexList[lang]).Batch(batch[lang])
				batch[lang] = (*docIndexes.indexList[lang]).NewBatch()
			}
		}

	}

	for _, lang := range languages {
		(*docIndexes.indexList[lang]).Batch(batch[lang])

		// Store the new seq number in the indexes
		SetStoreSeq(docIndexes.indexList[lang], response.LastSeq)

		// Send the new index to the search side
		err := Replicate(docIndexes.indexList[lang], search.SearchPrefixPath+lang+"/"+docIndexes.indexPath)
		if err != nil {
			fmt.Printf("Error on replication:  %s\n", err)
			continue
		}
	}

	return nil
}

func ReIndex() error {

	// Save indexes before reindexing
	ReplicateAll()

	// Close existing indexes
	for _, docIndexes := range indexes {
		for _, lang := range languages {
			(*docIndexes.indexList[lang]).Close()
		}
	}

	// Remove indexes
	os.RemoveAll(prefixPath)

	// Reopen index from scratch
	for _, docIndexes := range indexes {
		for _, lang := range languages {
			var err error
			docIndexes.indexList[lang], err = GetIndex(docIndexes.indexPath, lang, docIndexes.docType)
			if err != nil {
				fmt.Printf("Error on GetIndex:  %s\n", err)
				return err
			}
		}

		IndexUpdate(docIndexes)

	}

	return nil

}

func ReplicateAll() error {
	start := time.Now()
	var count uint64
	count = 0

	for _, lang := range languages {
		for _, docIndexes := range indexes {
			tmp, _ := (*docIndexes.indexList[lang]).DocCount()
			count += tmp
			fmt.Println("save/" + (*docIndexes.indexList[lang]).Name())
			err := Replicate(docIndexes.indexList[lang], "save/"+(*docIndexes.indexList[lang]).Name())
			if err != nil {
				fmt.Printf("Error on replication: %s\n", err)
				return err
			}
		}
	}

	fmt.Println("Storage replication time:", time.Since(start), "for", count, "documents")
	return nil
}

func Replicate(index *bleve.Index, path string) error {
	_, store, err := (*index).Advanced()
	if err != nil {
		fmt.Println(err)
		return err
	}
	r, err := store.Reader()
	if err != nil {
		fmt.Println(err)
		return err
	}

	// In case the query folder doesn't exist yet
	err = os.MkdirAll(path, 0700)
	if err != nil {
		fmt.Println(err)
		return err
	}

	tmpFile, err := ioutil.TempFile(path, "store.tmp.")
	if err != nil {
		fmt.Println(err)
		return err
	}

	err = r.WriteTo(tmpFile)
	if err != nil {
		fmt.Println(err)
		return err
	}
	err = tmpFile.Close()
	if err != nil {
		fmt.Println(err)
		return err
	}
	err = r.Close()
	if err != nil {
		fmt.Println(err)
		return err
	}

	if _, err := os.Stat(path + "/index_meta.json"); os.IsNotExist(err) {
		f, err := os.OpenFile(path+"/index_meta.json", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			fmt.Println(err)
			return err
		}
		f.WriteString("{\"storage\":\"boltdb\",\"index_type\":\"upside_down\"}")
		f.Close()
	}

	// TODO : put a lock on the rename if necessary (even though it is based on syscall.Rename on Posix systems)
	err = os.Rename(tmpFile.Name(), path+"/store")
	if err != nil {
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

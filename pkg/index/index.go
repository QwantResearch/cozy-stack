package index

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blevesearch/bleve"
	// "github.com/blevesearch/bleve/mapping"
	"github.com/cozy/cozy-stack/pkg/consts"
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

// var typeMap map[string]interface{}

var mapIndexType map[string]string
var indexAlias bleve.IndexAlias
var inst *instance.Instance

var photoAlbumIndex map[string]*bleve.Index
var fileIndex map[string]*bleve.Index
var bankAccountIndex map[string]*bleve.Index

var languages []string

var prefixPath string

var ft_language *FastText

func StartIndex(instance *instance.Instance) error {

	inst = instance

	mapIndexType = map[string]string{
		"photo.albums.bleve":  consts.PhotosAlbums,
		"file.bleve":          consts.Files,
		"bank.accounts.bleve": "io.cozy.bank.accounts", // TODO : check why it doesn't exist in consts
	}

	ft_language = NewFastTextInst()
	ft_language.LoadModel("pkg/index/lid.176.ftz")

	var err error

	languages = GetAvailableLanguages()

	photoAlbumIndex = make(map[string]*bleve.Index, len(languages))
	fileIndex = make(map[string]*bleve.Index, len(languages))
	bankAccountIndex = make(map[string]*bleve.Index, len(languages))

	prefixPath = "bleve/"

	for _, lang := range languages {
		photoAlbumIndex[lang], err = GetIndex("photo.albums.bleve", lang)
		if err != nil {
			return err
		}

		fileIndex[lang], err = GetIndex("file.bleve", lang)
		if err != nil {
			return err
		}

		bankAccountIndex[lang], err = GetIndex("bank.accounts.bleve", lang)
		if err != nil {
			return err
		}
	}

	IndexUpdate()

	// Creating an aliasIndex to make it clear to the user:

	indexAlias = bleve.NewIndexAlias()
	for _, i := range photoAlbumIndex {
		indexAlias.Add(*i)
	}
	for _, i := range fileIndex {
		indexAlias.Add(*i)
	}
	for _, i := range bankAccountIndex {
		indexAlias.Add(*i)
	}

	ReplicateAll()

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

func GetIndex(indexPath string, lang string) (*bleve.Index, error) {
	indexMapping := bleve.NewIndexMapping()
	AddTypeMapping(indexMapping, mapIndexType[indexPath], lang)

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

func IndexUpdate() error {

	count := 0
	for _, indexList := range []map[string]*bleve.Index{photoAlbumIndex, fileIndex, bankAccountIndex} {

		// Set request to get last changes
		name_index := strings.Split((*indexList["en"]).Name(), "/")
		docType := mapIndexType[name_index[len(name_index)-1]]
		last_store_seq, err := GetStoreSeq(indexList["en"])
		if err != nil {
			fmt.Printf("Error on GetStoredSeq: %s\n", err)
		}

		var request = &couchdb.ChangesRequest{
			DocType:     docType,
			Since:       last_store_seq, // Set with last seq
			IncludeDocs: true,
		}

		// Fetch last changes
		// TODO : check that we index last version when there are multiple changes for a doc since last seq
		response, err := couchdb.GetChanges(inst, request)
		if err != nil {
			fmt.Printf("Error on getChanges: %s\n", err)
			// return err
			continue
		}

		// Index thoses last changes
		batch := make(map[string]*bleve.Batch, len(languages))
		for _, lang := range languages {
			batch[lang] = (*indexList[lang]).NewBatch()
		}

		for i, result := range response.Results {

			// TODO : deal with files with trashed = true (remove them from index or not)

			if _, ok := result.Doc.M["name"]; !ok {
				// TODO : find out out why some changes don't correspond to files only and thus don't have "name" field
				fmt.Printf("Error on fetching name\n")
				continue
			}

			originalLang := FindWhichLangIndexDoc(indexList, result.DocID)
			if originalLang != "" {
				// We found the document so we should update it the original index
				result.Doc.M["docType"] = docType
				batch[originalLang].Index(result.DocID, result.Doc.M)
				count += 1
			} else {
				// We couldn't find the document, so we predict the language to index it in the right index
				pred, err := ft_language.GetLanguage(result.Doc.M["name"].(string)) // TODO: predict on content and not "name" field in particular
				if err != nil {
					fmt.Printf("Error on language prediction:  %s\n", err)
					continue
				}
				result.Doc.M["docType"] = docType
				batch[pred].Index(result.DocID, result.Doc.M)
				count += 1
			}

			// Batch files
			if i%300 == 0 {
				for _, lang := range languages {
					(*indexList[lang]).Batch(batch[lang])
					batch[lang] = (*indexList[lang]).NewBatch()
				}
			}

		}

		for _, lang := range languages {
			(*indexList[lang]).Batch(batch[lang])

			// Store the new seq number in the indexes
			SetStoreSeq(indexList[lang], response.LastSeq)
		}

		fmt.Println("Updated", count, "documents")

	}

	return nil
}

func ReIndex() error {

	os.RemoveAll(prefixPath)

	for _, lang := range languages {

		// Creating new indexes
		newPhotoAlbumIndex, err := GetIndex("photo.albums.bleve", lang)
		if err != nil {
			return err
		}

		newFileIndex, err := GetIndex("file.bleve", lang)
		if err != nil {
			return err
		}

		newBankAccountIndex, err := GetIndex("bank.accounts.bleve", lang)
		if err != nil {
			return err
		}

		// Swapping
		indexAlias.Swap(
			[]bleve.Index{(*newPhotoAlbumIndex), (*newFileIndex), (*newBankAccountIndex)},
			[]bleve.Index{*(photoAlbumIndex[lang]), *(fileIndex[lang]), *(bankAccountIndex[lang])})

		// Closing old indexes
		(*photoAlbumIndex[lang]).Close()
		(*fileIndex[lang]).Close()
		(*bankAccountIndex[lang]).Close()

		// Setting global var
		photoAlbumIndex[lang] = newPhotoAlbumIndex
		fileIndex[lang] = newFileIndex
		bankAccountIndex[lang] = newBankAccountIndex

	}

	return nil

}

func ReplicateAll() error {
	start := time.Now()
	var count uint64
	count = 0

	for _, lang := range languages {
		for _, index := range []map[string]*bleve.Index{fileIndex, photoAlbumIndex, bankAccountIndex} {
			tmp, _ := (*index[lang]).DocCount()
			count += tmp
			err := Replicate(index[lang], (*index[lang]).Name()+"/store.save")
			if err != nil {
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

	f, _ := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	err = r.WriteTo(f)
	if err != nil {
		fmt.Println(err)
		return err
	}
	err = f.Close()
	if err != nil {
		fmt.Println(err)
		return err
	}

	err = r.Close()
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

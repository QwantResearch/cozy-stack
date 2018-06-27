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
	"github.com/cozy/cozy-stack/pkg/realtime"
	"github.com/cozy/cozy-stack/pkg/vfs"
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

func StartIndex(instance *instance.Instance) error {

	inst = instance

	mapIndexType = map[string]string{
		"photo.albums.bleve":  consts.PhotosAlbums,
		"file.bleve":          consts.Files,
		"bank.accounts.bleve": "io.cozy.bank.accounts", // TODO : check why it doesn't exist in consts
	}

	LoadModel("pkg/index/lid.176.ftz")

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

	// subscribing to changes
	eventChan := realtime.GetHub().Subscriber(inst)
	for _, value := range mapIndexType {
		eventChan.Subscribe(value)
	}

	go func() {
		for ev := range eventChan.Channel {

			var originalIndex *bleve.Index
			originalIndex = nil
			if ev.Verb == "CREATED" {

				// TODO : Change casting to the right one
				// + solve the problem of finding which content to predict the language on
				doc := ev.Doc.(*vfs.FileDoc)

				// TODO : detect language on the fields depending on the doctype, and not just "name"
				lang := GetLanguage(doc.Name())

				if ev.Doc.DocType() == "io.cozy.photos.albums" {
					originalIndex = photoAlbumIndex[lang]
				}
				if ev.Doc.DocType() == "io.cozy.files" {
					originalIndex = fileIndex[lang]
				}
				if ev.Doc.DocType() == "io.cozy.bank.accounts" {
					originalIndex = bankAccountIndex[lang]
				}

				if originalIndex == nil {
					fmt.Println("DocType not supported")
					return
				}

				(*originalIndex).Index(ev.Doc.ID(), ev.Doc)
				fmt.Println(ev.Doc)
				fmt.Println("indexed")

			} else if ev.Verb == "UPDATED" {

				// To make it easier, we assume that language doesn't change and we find original index
				if ev.Doc.DocType() == "io.cozy.photos.albums" {
					originalIndex = FindIndexDoc(photoAlbumIndex, ev.Doc.ID())
				}
				if ev.Doc.DocType() == "io.cozy.files" {
					originalIndex = FindIndexDoc(fileIndex, ev.Doc.ID())
				}
				if ev.Doc.DocType() == "io.cozy.bank.accounts" {
					originalIndex = FindIndexDoc(bankAccountIndex, ev.Doc.ID())
				}

				if originalIndex == nil {
					fmt.Println("DocType not supported")
					return
				}

				(*originalIndex).Index(ev.Doc.ID(), ev.Doc)
				fmt.Println(ev.Doc)
				fmt.Println("reindexed")

			} else if ev.Verb == "DELETED" {

				if ev.Doc.DocType() == "io.cozy.photos.albums" {
					originalIndex = FindIndexDoc(photoAlbumIndex, ev.Doc.ID())
				}
				if ev.Doc.DocType() == "io.cozy.files" {
					originalIndex = FindIndexDoc(fileIndex, ev.Doc.ID())
				}
				if ev.Doc.DocType() == "io.cozy.bank.accounts" {
					originalIndex = FindIndexDoc(bankAccountIndex, ev.Doc.ID())
				}

				if originalIndex == nil {
					fmt.Println("DocType not supported")
					return
				}

				err := (*originalIndex).Delete(ev.Doc.ID())
				fmt.Println(err)
				fmt.Println("deleted")

			} else {
				fmt.Println(ev.Verb)
			}
		}
	}()

	return nil
}

func FindIndexDoc(indexList map[string]*bleve.Index, id string) *bleve.Index {
	for _, i := range indexList {
		doc, _ := (*i).Document(id)
		if doc != nil {
			return i
		}
	}
	return nil
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

		FillIndex(i, mapIndexType[indexPath], lang)

		return &i, nil

	} else if err1 != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", fullIndexPath, err1)
		return &i, err1
	}

	fmt.Println("found existing Index")
	return &i, nil
}

func FillIndex(index bleve.Index, docType string, lang string) {

	// Which solution to use ?
	// Either a common struct (such as JSONDoc) or a struct by type of document ?

	// 	// Specified struct

	// var docsFile []file
	// var docsPhotoAlbum []photoAlbum
	// if docType == "io.cozy.photos.albums" {
	// 	GetFileDocs(docType, &docsPhotoAlbum)
	// 	for i := range docsPhotoAlbum {
	// 		docsPhotoAlbum[i].DocType = docType
	// 		index.Index(docsPhotoAlbum[i].ID, docsPhotoAlbum[i])
	// 	}
	// } else {
	// 	GetFileDocs(docType, &docsFile)
	// 	for i := range docsFile {
	// 		docsFile[i].DocType = docType
	// 		index.Index(docsFile[i].ID, docsFile[i])
	// 	}
	// }

	// 	// Common struct

	// // Indexation Time
	// start := time.Now()
	// var docs []couchdb.JSONDoc
	// GetFileDocs(docType, &docs)
	// for i := range docs {
	// 	docs[i].M["DocType"] = docType
	// 	index.Index(docs[i].ID(), docs[i].M)
	// }
	// end := time.Since(start)
	// fmt.Println(docType, " indexing time: ", end, " for ", len(docs), " documents")

	// Indexation Batch Time
	start := time.Now()
	count := 0
	var docs []couchdb.JSONDoc
	batch := index.NewBatch()
	GetFileDocs(docType, &docs)
	for i := range docs {

		// TODO : detect language on the fields depending on the doctype, and not just "name"
		if GetLanguage(docs[i].M["name"].(string)) == lang {
			count += 1
			docs[i].M["DocType"] = docType
			batch.Index(docs[i].ID(), docs[i].M)
			if i%300 == 0 {
				index.Batch(batch)
				batch = index.NewBatch()
			}
		}
	}
	index.Batch(batch)
	end := time.Since(start)
	fmt.Println(docType, "indexing time:", end, "for", count, "documents", lang)

}

func GetFileDocs(docType string, docs interface{}) {
	req := &couchdb.AllDocsRequest{}
	err := couchdb.GetAllDocs(inst, docType, req, docs)
	if err != nil {
		fmt.Printf("Error on unmarshall: %s\n", err)
	}
}

func QueryIndex(queryString string) ([]couchdb.JSONDoc, error) {

	start := time.Now()
	var fetched []couchdb.JSONDoc

	query := bleve.NewQueryStringQuery(PreparingQuery(queryString))
	searchRequest := bleve.NewSearchRequest(query)

	// Addings Facets
	// docTypes facet
	searchRequest.AddFacet("docTypes", bleve.NewFacetRequest("DocType", 3))
	// created facet
	var cutOffDate = time.Now().Add(-7 * 24 * time.Hour)
	createdFacet := bleve.NewFacetRequest("created_at", 2)
	createdFacet.AddDateTimeRange("old", time.Unix(0, 0), cutOffDate)
	createdFacet.AddDateTimeRange("new", cutOffDate, time.Unix(9999999999, 9999999999)) //check how many 9 needed
	searchRequest.AddFacet("created", createdFacet)

	searchResults, err := indexAlias.Search(searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s", err)
		return fetched, err
	}
	fmt.Printf(searchResults.String())

	for _, dateRange := range searchResults.Facets["created"].DateRanges {
		fmt.Printf("\t%s(%d)\n", dateRange.Name, dateRange.Count)
	}

	var currFetched couchdb.JSONDoc
	for _, result := range searchResults.Hits {
		// TODO : check that the hits are not the 10 first
		currFetched = couchdb.JSONDoc{}
		couchdb.GetDoc(inst, mapIndexType[result.Index[strings.LastIndex(result.Index, "/")+1:]], result.ID, &currFetched)
		fetched = append(fetched, currFetched)
	}

	end := time.Since(start)
	fmt.Println("query time:", end)

	return fetched, nil
}

func PreparingQuery(queryString string) string {
	return "*" + queryString + "*"
}

func ReIndex() error {

	for _, lang := range languages {

		os.RemoveAll(prefixPath)

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

		// Closing all indexes
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

package index

import (
	"fmt"
	"time"

	"github.com/blevesearch/bleve"
	// "github.com/blevesearch/bleve/mapping"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/instance"
	"github.com/cozy/cozy-stack/pkg/realtime"
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

func StartIndex(instance *instance.Instance) error {
	inst = instance

	mapIndexType = map[string]string{
		"bleve/photo.albums.bleve":  consts.PhotosAlbums,
		"bleve/file.bleve":          consts.Files,
		"bleve/bank.accounts.bleve": "io.cozy.bank.accounts", // TODO : check why it doesn't exist in consts
	}

	LoadModel("pkg/index/lid.176.ftz")

	var err error

	languages := GetAvailableLanguages()

	photoAlbumIndex := make(map[string]*bleve.Index, len(languages))
	fileIndex := make(map[string]*bleve.Index, len(languages))
	bankAccountIndex := make(map[string]*bleve.Index, len(languages))

	for _, lang := range languages {
		photoAlbumIndex[lang], err = GetIndex("bleve/photo.albums.bleve", lang)
		if err != nil {
			return err
		}

		fileIndex[lang], err = GetIndex("bleve/file.bleve", lang)
		if err != nil {
			return err
		}

		bankAccountIndex[lang], err = GetIndex("bleve/bank.accounts.bleve", lang)
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

			doc := ev.Doc.(couchdb.JSONDoc)
			lang := GetLanguage(doc.M["name"].(string))

			var originalIndex *bleve.Index
			if ev.Doc.DocType() == "io.cozy.photos.albums" {
				originalIndex = photoAlbumIndex[lang]
			}
			if ev.Doc.DocType() == "io.cozy.files" {
				originalIndex = fileIndex[lang]
			}
			if ev.Doc.DocType() == "io.cozy.bank.accounts" {
				originalIndex = bankAccountIndex[lang]
			}
			if ev.Verb == "CREATED" || ev.Verb == "UPDATED" {
				(*originalIndex).Index(ev.Doc.ID(), ev.Doc)
				fmt.Println(ev.Doc)
				fmt.Println("reindexed")
			} else if ev.Verb == "DELETED" {
				indexAlias.Delete(ev.Doc.ID())
				fmt.Println("deleted")
			} else {
				fmt.Println(ev.Verb)
			}
		}
	}()

	return nil
}

func GetIndex(indexPath string, lang string) (*bleve.Index, error) {
	indexMapping := bleve.NewIndexMapping()
	AddTypeMapping(indexMapping, mapIndexType[indexPath], lang)

	blevePath := indexPath

	i, err1 := bleve.Open(blevePath + "." + lang)

	// Create it if it doesn't exist
	if err1 == bleve.ErrorIndexPathDoesNotExist {
		fmt.Printf("Creating new index %s...\n", indexPath)
		i, err2 := bleve.New(blevePath+"."+lang, indexMapping)
		if err2 != nil {
			fmt.Printf("Error on creating new Index: %s\n", err2)
			return &i, err2
		}
		FillIndex(i, mapIndexType[indexPath], lang)
		return &i, nil

	} else if err1 != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", indexPath, err1)
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
	var docs []couchdb.JSONDoc
	batch := index.NewBatch()
	GetFileDocs(docType, &docs)
	for i := range docs {
		if GetLanguage(docs[i].M["name"].(string)) == lang {
			docs[i].M["DocType"] = docType
			batch.Index(docs[i].ID(), docs[i].M)
		}
	}
	index.Batch(batch)
	end := time.Since(start)
	fmt.Println(docType, "indexing time:", end, "for", len(docs), "documents", lang)

}

func GetFileDocs(docType string, docs interface{}) {
	req := &couchdb.AllDocsRequest{}
	err := couchdb.GetAllDocs(inst, docType, req, docs)
	if err != nil {
		fmt.Printf("Error on unmarshall: %s\n", err)
	}
}

func QueryIndex(queryString string) ([]couchdb.JSONDoc, error) {
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
		currFetched = couchdb.JSONDoc{}
		couchdb.GetDoc(inst, mapIndexType[result.Index], result.ID, &currFetched)
		fetched = append(fetched, currFetched)
	}

	return fetched, nil
}

func PreparingQuery(queryString string) string {
	return "*" + queryString + "*"
}

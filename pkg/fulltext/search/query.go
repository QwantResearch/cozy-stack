package search

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/web/jsonapi"
)

type QueryRequest struct {
	QueryString string `json:"queryString"`
	NumbResults int    `json:"numbResults"`
	Highlight   bool   `json:"highlight"`
	Name        bool   `json:"name"`
	Rev         bool   `json:"_rev"`
	Offset      int    `json:"offset"`
}

type SearchResult struct {
	id        string `json:"_id"`
	docType   string `json:"docType"`
	rev       string `json:"_rev"`
	Name      string `json:"name"`
	Highlight string `json:"highlight"`
}

func (r *SearchResult) Rev() string                            { return r.rev }
func (r *SearchResult) ID() string                             { return r.id }
func (r *SearchResult) DocType() string                        { return r.docType }
func (r *SearchResult) Clone() couchdb.Doc                     { cloned := *r; return &cloned }
func (r *SearchResult) SetRev(rev string)                      { r.rev = rev }
func (r *SearchResult) SetID(id string)                        { r.id = id }
func (r *SearchResult) Relationships() jsonapi.RelationshipMap { return nil }
func (r *SearchResult) Included() []jsonapi.Object             { return []jsonapi.Object{} }
func (r *SearchResult) MarshalJSON() ([]byte, error)           { return json.Marshal(*r) }
func (r *SearchResult) Links() *jsonapi.LinksList              { return nil }

const (
	SearchPrefixPath = "bleve/query/"
)

func OpenIndexAlias() (bleve.IndexAlias, []*bleve.Index, error) {

	// Deal with languages and docTypes dynamically instead
	languages := []string{"fr", "en"}
	docTypeList := []string{consts.Files}

	var indexes []*bleve.Index

	indexAlias := bleve.NewIndexAlias()

	for _, lang := range languages {
		for _, docType := range docTypeList {
			path := SearchPrefixPath + lang + "/" + docType
			index, err := bleve.Open(path)
			if err == bleve.ErrorIndexMetaMissing {
				CreateMetaIndexJson(path)
				index, err = bleve.Open(path)
			}
			if err != nil {
				fmt.Printf("Error on opening index: %s\n", err)
				// TODO : deal with thar error better in case of index not ready yet
				return nil, nil, err
			}
			indexes = append(indexes, &index)
			indexAlias.Add(index)
		}
	}

	return indexAlias, indexes, nil
}

func CloseIndexAlias(indexAlias bleve.IndexAlias, indexes []*bleve.Index) {
	for _, index := range indexes {
		indexAlias.Remove((*index))
		(*index).Close()
	}

}

func QueryIndex(request QueryRequest) ([]SearchResult, int, error) {

	start := time.Now()

	indexAlias, indexes, err := OpenIndexAlias()
	if err != nil {
		fmt.Printf("Error when opening indexAlias: %s\n", err)
		return nil, 0, err
	}

	searchRequest := BuildQuery(request, false)

	searchResults, err := indexAlias.Search(searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s\n", err)
		return nil, 0, err
	}

	CloseIndexAlias(indexAlias, indexes)

	for _, dateRange := range searchResults.Facets["created"].DateRanges {
		fmt.Printf("\t%s(%d)\n", dateRange.Name, dateRange.Count)
	}

	fetched := BuildResults(request, searchResults)

	end := time.Since(start)
	fmt.Println("query time:", end)

	return fetched, int(searchResults.Total), nil
}

func PreparingQuery(queryString string) string {
	return "*" + queryString + "*"
}

func QueryPrefixIndex(request QueryRequest) ([]SearchResult, int, error) {

	indexAlias, indexes, err := OpenIndexAlias()
	if err != nil {
		fmt.Printf("Error when opening indexAlias: %s\n", err)
		return nil, 0, err
	}

	searchRequest := BuildQuery(request, true)

	searchResults, err := indexAlias.Search(searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s\n", err)
		return nil, 0, err
	}

	CloseIndexAlias(indexAlias, indexes)

	fetched := BuildResults(request, searchResults)

	return fetched, int(searchResults.Total), nil
}

func BuildQuery(request QueryRequest, prefix bool) *bleve.SearchRequest {

	var searchRequest *bleve.SearchRequest
	if prefix {
		query := bleve.NewMatchPhrasePrefixQuery(request.QueryString)
		searchRequest = bleve.NewSearchRequest(query)
	} else {
		query := bleve.NewQueryStringQuery(PreparingQuery(request.QueryString))
		searchRequest = bleve.NewSearchRequest(query)
	}

	if request.Highlight {
		searchRequest.Fields = []string{"*"} // instead of being all fields, it should be all indexed fields.
		searchRequest.Highlight = bleve.NewHighlight()
	} else {
		searchRequest.Fields = []string{"docType"}
		if request.Name {
			searchRequest.Fields = append(searchRequest.Fields, "name")
		}
		if request.Rev {
			searchRequest.Fields = append(searchRequest.Fields, "_rev")
		}
	}
	searchRequest.Size = request.NumbResults
	searchRequest.From = request.Offset

	// Addings Facets
	// docTypes facet
	searchRequest.AddFacet("docTypes", bleve.NewFacetRequest("docType", 3))
	// created facet
	var cutOffDate = time.Now().Add(-7 * 24 * time.Hour)
	createdFacet := bleve.NewFacetRequest("created_at", 2)
	createdFacet.AddDateTimeRange("old", time.Unix(0, 0), cutOffDate)
	createdFacet.AddDateTimeRange("new", cutOffDate, time.Unix(9999999999, 9999999999)) //check how many 9 needed
	searchRequest.AddFacet("created", createdFacet)

	return searchRequest
}

func BuildResults(request QueryRequest, searchResults *bleve.SearchResult) []SearchResult {
	fetched := make([]SearchResult, len(searchResults.Hits))
	for i, result := range searchResults.Hits {
		currFetched := SearchResult{result.ID, (result.Fields["docType"]).(string), "", "", ""}

		if request.Highlight {
			currFetched.Highlight = result.Fragments["name"][0] //TODO deal with Fragments better, what if not on name field ?
		}
		if request.Name {
			currFetched.Name = result.Fields["name"].(string)
		}
		if request.Rev {
			currFetched.SetRev(result.Fields["_rev"].(string))
		}
		// currFetched := SearchResult{result.ID, (result.Fields["_rev"]).(string), (result.Fields["docType"]).(string), (result.Fields["name"]).(string), result.Fragments["name"][0]}
		fetched[i] = currFetched
	}

	return fetched
}

func CreateMetaIndexJson(path string) error {
	f, err := os.OpenFile(path+"/index_meta.json", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Println(err)
		return err
	}
	f.WriteString("{\"storage\":\"boltdb\",\"index_type\":\"upside_down\"}")
	f.Close()
	return nil
}

package index

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/web/jsonapi"
)

type SearchResult struct {
	id        string `json:"_id"`
	rev       string `json:"_rev"`
	docType   string `json:"docType"`
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

func QueryIndex(queryString string) ([]SearchResult, int, error) {

	start := time.Now()
	numb_results := 15

	query := bleve.NewQueryStringQuery(PreparingQuery(queryString))
	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Fields = []string{"*"}
	searchRequest.Highlight = bleve.NewHighlight()
	searchRequest.Size = numb_results

	// Addings Facets
	// docTypes facet
	searchRequest.AddFacet("docTypes", bleve.NewFacetRequest("docType", 3))
	// created facet
	var cutOffDate = time.Now().Add(-7 * 24 * time.Hour)
	createdFacet := bleve.NewFacetRequest("created_at", 2)
	createdFacet.AddDateTimeRange("old", time.Unix(0, 0), cutOffDate)
	createdFacet.AddDateTimeRange("new", cutOffDate, time.Unix(9999999999, 9999999999)) //check how many 9 needed
	searchRequest.AddFacet("created", createdFacet)

	searchResults, err := indexAlias.Search(searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s", err)
		return nil, 0, err
	}
	fmt.Printf(searchResults.String())

	for _, dateRange := range searchResults.Facets["created"].DateRanges {
		fmt.Printf("\t%s(%d)\n", dateRange.Name, dateRange.Count)
	}

	fetched := make([]SearchResult, len(searchResults.Hits))
	for i, result := range searchResults.Hits {
		// TODO : check that the hits are not the 10 first
		currFetched := SearchResult{result.ID, (result.Fields["_rev"]).(string), (result.Fields["docType"]).(string), (result.Fields["name"]).(string), result.Fragments["name"][0]}
		fetched[i] = currFetched
	}

	end := time.Since(start)
	fmt.Println("query time:", end)

	return fetched, int(searchResults.Total), nil
}

func PreparingQuery(queryString string) string {
	return "*" + queryString + "*"
}

func QueryPrefixIndex(queryString string) ([]SearchResult, int, error) {

	numb_results := 15

	query := bleve.NewMatchPhrasePrefixQuery(queryString)
	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Fields = []string{"*"}
	searchRequest.Highlight = bleve.NewHighlight()
	searchRequest.Size = numb_results

	searchResults, err := indexAlias.Search(searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s", err)
		return nil, 0, err
	}
	fmt.Printf(searchResults.String())

	fetched := make([]SearchResult, len(searchResults.Hits))
	for i, result := range searchResults.Hits {
		// TODO : check that the hits are not the 10 first
		currFetched := SearchResult{result.ID, (result.Fields["_rev"]).(string), (result.Fields["docType"]).(string), (result.Fields["name"]).(string), result.Fragments["name"][0]}
		fetched[i] = currFetched
	}

	return fetched, int(searchResults.Total), nil
}

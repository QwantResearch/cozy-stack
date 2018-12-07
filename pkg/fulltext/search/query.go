package search

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"time"

	"github.com/blevesearch/bleve"
)

type QueryRequest struct {
	QueryString  string   `json:"queryString"`
	NumbResults  int      `json:"numbResults"`
	Highlight    bool     `json:"highlight"`
	Name         bool     `json:"name"`
	Rev          bool     `json:"_rev"`
	Offset       int      `json:"offset"`
	Order        []string `json:"order"`
	DocTypes     []string `json:"docTypes"`
	InstanceName string   `json:"instance"`
}

type SearchResult struct {
	Id        string              `json:"_id"`
	DocType   string              `json:"_type"`
	Rev       string              `json:"_rev"`
	Name      string              `json:"name"`
	Highlight map[string][]string `json:"html_highlight"`
}

const (
	SearchPrefixPath = "bleve/query/"
)

var waitDuration = 2 * time.Second

func OpenIndexAlias(instName string, docTypeList []string) (bleve.IndexAlias, []*bleve.Index, error) {

	if len(docTypeList) == 0 {
		return nil, nil, fmt.Errorf("DocTypeList can't be empty")
	}

	// Deal with languages and docTypes dynamically instead
	languages, err := GetLanguageInstance(instName)
	if err != nil {
		return nil, nil, err
	}

	var indexes []*bleve.Index

	indexAlias := bleve.NewIndexAlias()

	for _, lang := range languages {
		for _, docType := range docTypeList {
			indexPath := path.Join(SearchPrefixPath, instName, lang, docType)
			index, err := bleve.OpenUsing(indexPath, map[string]interface{}{
				"read_only": true,
			})
			if err == bleve.ErrorIndexMetaMissing {
				err2 := CreateMetaIndexJson(indexPath)
				if err2 != nil {
					fmt.Printf("Error on CreateMetaIndexJson: %s\n", err2)
					return nil, nil, err2
				}
				index, err2 = bleve.OpenUsing(indexPath, map[string]interface{}{
					"read_only": true,
				})
				if err2 != nil {
					fmt.Printf("Error on opening index: %s\n", err2)
					return nil, nil, err2
				}
			} else if err != nil {
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

	indexAlias, indexes, err := OpenIndexAlias(request.InstanceName, request.DocTypes)
	if err != nil {
		fmt.Printf("Error when opening indexAlias: %s\n", err)
		return nil, 0, err
	}

	searchRequest := BuildQuery(request, false)

	searchResults, err := SearchWithTimeout(indexAlias, searchRequest)
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

	indexAlias, indexes, err := OpenIndexAlias(request.InstanceName, request.DocTypes)
	if err != nil {
		fmt.Printf("Error when opening indexAlias: %s\n", err)
		return nil, 0, err
	}

	searchRequest := BuildQuery(request, true)

	searchResults, err := SearchWithTimeout(indexAlias, searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s\n", err)
		return nil, 0, err
	}

	CloseIndexAlias(indexAlias, indexes)

	fetched := BuildResults(request, searchResults)

	return fetched, int(searchResults.Total), nil
}

func SearchWithTimeout(indexAlias bleve.IndexAlias, searchRequest *bleve.SearchRequest) (*bleve.SearchResult, error) {
	// We use SearchInContext to set a timeout on search.
	// See: https://groups.google.com/d/msg/bleve/f7Qnhb9qfoM/S6mUS3HuAwAJ
	// And: https://groups.google.com/d/msg/bleve/U6MtvUK_sVI/JOc2tsjuAwAJ

	ctx, _ := context.WithTimeout(context.Background(), waitDuration)
	searchResults, err := indexAlias.SearchInContext(ctx, searchRequest)
	if err != nil {
		fmt.Printf("Error on querying: %s\n", err)
		return nil, err
	}

	if searchResults.Status.Successful == 0 {
		// We consider that we return an error only if there was no success at all
		// Indeed, some of the indexes may have return a result before timeout and we should still return these

		fmt.Printf("No search result successful: %s\n", context.DeadlineExceeded)
		return nil, context.DeadlineExceeded
	}

	return searchResults, nil
}

func BuildQuery(request QueryRequest, prefix bool) *bleve.SearchRequest {

	var searchRequest *bleve.SearchRequest
	if prefix {
		query := bleve.NewMatchPhrasePrefixQuery(request.QueryString)
		searchRequest = bleve.NewSearchRequest(query)
	} else {
		query := bleve.NewQueryStringQuery(request.QueryString)
		searchRequest = bleve.NewSearchRequest(query)
	}

	searchRequest.Fields = []string{"docType"}
	if request.Name {
		searchRequest.Fields = append(searchRequest.Fields, "name")
	}
	if request.Rev {
		searchRequest.Fields = append(searchRequest.Fields, "_rev")
	}

	if request.Highlight {
		searchRequest.IncludeLocations = true
		searchRequest.Highlight = bleve.NewHighlight()
	}

	searchRequest.Size = request.NumbResults
	searchRequest.From = request.Offset

	if request.Order != nil {
		searchRequest.SortBy(request.Order)
	}

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
		currFetched := SearchResult{result.ID, (result.Fields["docType"]).(string), "", "", nil}

		if request.Highlight {
			currFetched.Highlight = result.Fragments
		}
		if request.Name {
			if name, ok := result.Fields["name"]; ok {
				currFetched.Name = name.(string)
			}
		}
		if request.Rev {
			if rev, ok := result.Fields["_rev"]; ok {
				currFetched.Rev = rev.(string)
			}
		}
		// currFetched := SearchResult{result.ID, (result.Fields["_rev"]).(string), (result.Fields["docType"]).(string), (result.Fields["name"]).(string), result.Fragments["name"][0]}
		fetched[i] = currFetched
	}

	return fetched
}

func CreateMetaIndexJson(indexPath string) error {
	f, err := os.OpenFile(path.Join(indexPath, "/index_meta.json"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Println(err)
		return err
	}

	_, err = f.WriteString("{\"storage\":\"boltdb\",\"index_type\":\"upside_down\"}")
	if err != nil {
		return err
	}

	return f.Close()
}

func GetLanguageInstance(instName string) ([]string, error) {
	dirs, err := ioutil.ReadDir(path.Join(SearchPrefixPath, instName))
	if err != nil {
		return nil, err
	}

	languages := []string{}

	for _, dir := range dirs {
		if dir.IsDir() {
			languages = append(languages, dir.Name())
		}
	}
	return languages, nil
}

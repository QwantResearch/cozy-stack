package index

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	// "github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/web/jsonapi"
	// "github.com/cozy/cozy-stack/web/middlewares"
	"github.com/cozy/cozy-stack/pkg/index/indexation"
	"github.com/cozy/cozy-stack/pkg/index/search"
	// "github.com/cozy/cozy-stack/web/permissions"
	// "github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/echo"
)

func Routes(router *echo.Group) {
	router.POST("/_search", SearchQuery)
	router.POST("/_search_prefix", SearchQueryPrefix)
	router.POST("/_reindex", Reindex)
	router.POST("/_index_update", IndexUpdate)
	router.POST("/_update_index_alias/:doctype/:lang", ReplicateIndex)
}

func SearchQuery(c echo.Context) error {

	// instance := middlewares.GetInstance(c)
	var findRequest map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&findRequest); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return jsonapi.NewError(http.StatusBadRequest, "Could not decode the request")
	}

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	request := MakeRequest(findRequest)

	results, total, _ := search.QueryIndex(request)

	out := make([]jsonapi.Object, len(results))
	for i, result := range results {
		fmt.Println(result.Name)
		out[i] = &results[i]
	}

	// TODO : return the right needed infos
	return jsonapi.DataListWithTotal(c, http.StatusOK, total, out, nil)

}

func SearchQueryPrefix(c echo.Context) error {

	// instance := middlewares.GetInstance(c)
	var findRequest map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&findRequest); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return jsonapi.NewError(http.StatusBadRequest, "Could not decode the request")
	}

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	request := MakeRequest(findRequest)

	results, total, _ := search.QueryPrefixIndex(request)

	out := make([]jsonapi.Object, len(results))
	for i, result := range results {
		fmt.Println(result.Name)
		out[i] = &results[i]
	}

	// TODO : return the right needed infos
	return jsonapi.DataListWithTotal(c, http.StatusOK, total, out, nil)

}

func Reindex(c echo.Context) error {

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	err := indexation.ReIndex()
	if err != nil {
		fmt.Printf("Error on opening index: %s\n", err)
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	return jsonapi.DataList(c, http.StatusOK, nil, nil)

}

func IndexUpdate(c echo.Context) error {

	err := indexation.AllIndexesUpdate()
	if err != nil {
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	return jsonapi.DataList(c, http.StatusOK, nil, nil)

}

func ReplicateIndex(c echo.Context) error {

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	docType := c.Param("doctype")
	lang := c.Param("lang")

	path := search.SearchPrefixPath + lang + "/" + docType

	err := os.MkdirAll(path, 0700)
	if err != nil {
		fmt.Println(err)
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	tmpFile, err := ioutil.TempFile(path, "store.tmp.")
	if err != nil {
		fmt.Println(err)
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	_, err = io.Copy(tmpFile, c.Request().Body)
	if err != nil {
		fmt.Println(err)
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	err = tmpFile.Close()
	if err != nil {
		fmt.Println(err)
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	err = os.Rename(tmpFile.Name(), path+"/store")
	if err != nil {
		fmt.Println(err)
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	return jsonapi.DataList(c, http.StatusOK, nil, nil)
}

func MakeRequest(mapJSONRequest map[string]interface{}) search.QueryRequest {
	request := search.QueryRequest{
		QueryString: mapJSONRequest["searchQuery"].(string),
		// default values
		NumbResults: 15,
		Highlight:   true,
		Name:        true,
		Rev:         true,
	}

	if numbResults, ok := mapJSONRequest["numbResults"]; ok {
		request.NumbResults = int(numbResults.(float64))
	}

	if highlight, ok := mapJSONRequest["highlight"]; ok {
		request.Highlight = highlight.(bool)
	}

	if name, ok := mapJSONRequest["name"]; ok {
		request.Name = name.(bool)
	}

	if rev, ok := mapJSONRequest["_rev"]; ok {
		request.Rev = rev.(bool)
	}

	return request
}

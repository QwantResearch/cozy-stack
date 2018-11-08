package fulltext

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/web/jsonapi"
	// "github.com/cozy/cozy-stack/web/middlewares"
	"github.com/cozy/cozy-stack/pkg/fulltext/indexation"
	"github.com/cozy/cozy-stack/pkg/fulltext/search"
	// "github.com/cozy/cozy-stack/web/permissions"
	"github.com/cozy/echo"
)

func Routes(router *echo.Group) {
	router.POST("/_search", SearchQuery)
	router.POST("/_search_prefix", SearchQueryPrefix)
	router.POST("/_reindex", Reindex)
	router.POST("/_all_indexes_update", AllIndexesUpdate)
	router.POST("/_index_update", IndexUpdate)
	router.POST("/_update_index_alias/:instance/:doctype/:lang", ReplicateIndex)
	router.POST("/_delete_index", DeleteIndex)
	router.POST("/_delete_all_indexes", DeleteAllIndexes)
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

	results, _, _ := search.QueryIndex(request)

	return c.JSON(http.StatusOK, map[string]interface{}{"results": results, "query": findRequest})
}

func SearchQueryPrefix(c echo.Context) error {

	// instance := middlewares.GetInstance(c)
	var findRequest map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&findRequest); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": errors.New("Could not decode the request").Error(),
		})
	}

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	request := MakeRequest(findRequest)

	results, _, _ := search.QueryPrefixIndex(request)

	return c.JSON(http.StatusOK, map[string]interface{}{"results": results, "query": findRequest})
}

func Reindex(c echo.Context) error {

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	var body map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": errors.New("Could not decode the request").Error(),
		})
	}

	var instanceName string
	var ok bool
	if instanceName, ok = body["instance"].(string); !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": errors.New("instance string field required.").Error(),
		})
	}

	err := indexation.ReIndex(instanceName)
	if err != nil {
		fmt.Printf("Error on opening index: %s\n", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, nil)
}

func AllIndexesUpdate(c echo.Context) error {

	err := indexation.AllIndexesUpdate()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, nil)

}

func IndexUpdate(c echo.Context) error {

	var body map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": errors.New("Could not decode the request").Error(),
		})
	}

	var doctypeUpdate string
	var userID string
	var ok bool
	if doctypeUpdate, ok = body["docType"].(string); !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": errors.New("docType string field required.").Error(),
		})
	}

	if userID, ok = body["userID"].(string); !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": errors.New("userID string field required.").Error(),
		})
	}

	err := indexation.AddUpdateIndexJob(userID, doctypeUpdate)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, nil)
}

func ReplicateIndex(c echo.Context) error {

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	docType := c.Param("doctype")
	lang := c.Param("lang")
	instName := c.Param("instance")

	path := search.SearchPrefixPath + instName + "/" + lang + "/" + docType

	err := os.MkdirAll(path, 0700)
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	tmpFile, err := ioutil.TempFile(path, "store.tmp.")
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	_, err = io.Copy(tmpFile, c.Request().Body)
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	err = tmpFile.Close()
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	err = os.Rename(tmpFile.Name(), path+"/store")
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, nil)
}

func DeleteIndex(c echo.Context) error {

	var body map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": errors.New("Could not decode the request").Error(),
		})
	}

	var docType string
	var instance string
	var ok bool
	if docType, ok = body["docType"].(string); !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": errors.New("docType string field required.").Error(),
		})
	}

	if instance, ok = body["instance"].(string); !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": errors.New("instance string field required.").Error(),
		})
	}

	err := indexation.DeleteIndex(instance, docType)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, nil)
}

func DeleteAllIndexes(c echo.Context) error {

	var body map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		fmt.Printf("Error on decoding request: %s\n", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": errors.New("Could not decode the request").Error(),
		})
	}

	var instance string
	var ok bool

	if instance, ok = body["instance"].(string); !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": errors.New("instance string field required.").Error(),
		})
	}

	err := indexation.DeleteAllIndexesInstance(instance)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, nil)
}

func MakeRequest(mapJSONRequest map[string]interface{}) search.QueryRequest {

	request := search.QueryRequest{
		QueryString:  mapJSONRequest["searchQuery"].(string), // TODO: Deal with errors
		InstanceName: mapJSONRequest["instance"].(string),    // TODO: Deal with errors
		// default values
		NumbResults: 15,
		Highlight:   true,
		Name:        true,
		Rev:         true,
		Offset:      0,
		DocTypes:    []string{consts.Files}, // TODO : add all default doctypes
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

	if offset, ok := mapJSONRequest["offset"]; ok {
		request.Offset = int(offset.(float64))
	}

	if docTypes, ok := mapJSONRequest["docTypes"].([]interface{}); ok {
		request.DocTypes = make([]string, len(docTypes))
		for i, d := range docTypes {
			request.DocTypes[i] = d.(string)
		}
	}

	return request
}

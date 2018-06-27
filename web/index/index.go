package index

import (
	"encoding/json"
	"fmt"
	"net/http"

	// "github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/web/jsonapi"
	// "github.com/cozy/cozy-stack/web/middlewares"
	"github.com/cozy/cozy-stack/pkg/index"
	// "github.com/cozy/cozy-stack/web/permissions"
	// "github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/echo"
)

func Routes(router *echo.Group) {
	router.POST("/_search", SearchQuery)
	router.POST("/_search_prefix", SearchQueryPrefix)
	router.POST("/_reindex", Reindex)
}

func SearchQuery(c echo.Context) error {

	// instance := middlewares.GetInstance(c)
	var findRequest map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&findRequest); err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	results, _ := index.QueryIndex(fmt.Sprint(findRequest["searchQuery"]))

	for _, result := range results {
		fmt.Println(result.M["name"])
	}

	// TODO : return the right needed infos
	return jsonapi.DataList(c, http.StatusOK, nil, nil)

}

func SearchQueryPrefix(c echo.Context) error {

	// instance := middlewares.GetInstance(c)
	var findRequest map[string]interface{}

	if err := json.NewDecoder(c.Request().Body).Decode(&findRequest); err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	results, _ := index.QueryPrefixIndex(fmt.Sprint(findRequest["searchQuery"]))

	for _, result := range results {
		fmt.Println(result.M["name"])
	}

	// TODO : return the right needed infos
	return jsonapi.DataList(c, http.StatusOK, nil, nil)

}

func Reindex(c echo.Context) error {

	// TODO : see how to deal with permissions
	// if err := permissions.AllowWholeType(c, permissions.POST, consts.Files); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	err := index.ReIndex()
	if err != nil {
		return jsonapi.DataList(c, http.StatusInternalServerError, nil, nil)
	}

	return jsonapi.DataList(c, http.StatusOK, nil, nil)

}

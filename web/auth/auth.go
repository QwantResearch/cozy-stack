// Package auth provides register and login handlers
package auth

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/cozy/cozy-stack/apps"
	"github.com/cozy/cozy-stack/couchdb"
	"github.com/cozy/cozy-stack/web/jsonapi"
	"github.com/cozy/cozy-stack/web/middlewares"
	"github.com/labstack/echo"
)

func redirectSuccessLogin(c echo.Context, redirect string) error {
	instance := middlewares.GetInstance(c)

	session, err := NewSession(instance)
	if err != nil {
		return err
	}

	cookie, err := session.ToCookie()
	if err != nil {
		return err
	}

	c.SetCookie(cookie)
	return c.Redirect(http.StatusSeeOther, redirect)
}

func register(c echo.Context) error {
	instance := middlewares.GetInstance(c)

	registerToken, err := hex.DecodeString(c.FormValue("registerToken"))
	if err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	passphrase := []byte(c.FormValue("passphrase"))
	if err := instance.RegisterPassphrase(passphrase, registerToken); err != nil {
		return jsonapi.BadRequest(err)
	}

	return redirectSuccessLogin(c, instance.SubDomain(apps.OnboardingSlug))
}

func loginForm(c echo.Context) error {
	instance := middlewares.GetInstance(c)

	redirect, err := checkRedirectParam(c, instance.SubDomain(apps.HomeSlug))
	if err != nil {
		return err
	}

	if IsLoggedIn(c) {
		return c.Redirect(http.StatusSeeOther, redirect)
	}

	return c.Render(http.StatusOK, "login.html", echo.Map{
		"InvalidPassphrase": false,
		"Redirect":          redirect,
	})
}

func login(c echo.Context) error {
	instance := middlewares.GetInstance(c)

	redirect, err := checkRedirectParam(c, instance.SubDomain(apps.HomeSlug))
	if err != nil {
		return err
	}

	if IsLoggedIn(c) {
		return c.Redirect(http.StatusSeeOther, redirect)
	}

	passphrase := []byte(c.FormValue("passphrase"))
	if err := instance.CheckPassphrase(passphrase); err == nil {
		return redirectSuccessLogin(c, redirect)
	}

	return c.Render(http.StatusUnauthorized, "login.html", echo.Map{
		"InvalidPassphrase": true,
		"Redirect":          redirect,
	})
}

func logout(c echo.Context) error {
	// TODO check that a valid CtxToken is given to protect against CSRF attacks
	instance := middlewares.GetInstance(c)

	session, err := GetSession(c)
	if err == nil {
		c.SetCookie(session.Delete(instance))
	}

	return c.Redirect(http.StatusSeeOther, instance.PageURL("/auth/login"))
}

// checkRedirectParam returns the optional redirect query parameter. If not
// empty, we check that the redirect is a subdomain of the cozy-instance.
func checkRedirectParam(c echo.Context, defaultRedirect string) (string, error) {
	redirect := c.FormValue("redirect")
	if redirect == "" {
		redirect = defaultRedirect
	}

	u, err := url.Parse(redirect)
	if err != nil {
		return "", echo.NewHTTPError(http.StatusBadRequest,
			"bad url: could not parse")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", echo.NewHTTPError(http.StatusBadRequest,
			"bad url: bad scheme")
	}

	instance := middlewares.GetInstance(c)
	parts := strings.SplitN(u.Host, ".", 2)
	if len(parts) != 2 || parts[1] != instance.Domain || parts[0] == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest,
			"bad url: should be subdomain")
	}

	// To protect against stealing authorization code with redirection, the
	// fragment is always overriden. Most browsers keep URI fragments upon
	// redirects, to make sure to override them, we put an empty one.
	//
	// see: oauthsecurity.com/#provider-in-the-middle
	// see: 7.4.2 OAuth2 in Action
	u.Fragment = ""
	return u.String() + "#", nil
}

func registerClient(c echo.Context) error {
	// TODO add rate-limiting to prevent DOS attacks
	if c.Request().Header.Get("Content-Type") != "application/json" {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": "bad_content_type",
		})
	}
	client := new(Client)
	if err := c.Bind(client); err != nil {
		return err
	}
	instance := middlewares.GetInstance(c)
	if err := client.Create(instance); err != nil {
		return c.JSON(err.Code, err)
	}
	return c.JSON(http.StatusCreated, client)
}

func authorizeForm(c echo.Context) error {
	instance := middlewares.GetInstance(c)
	responseType := c.QueryParam("response_type")
	state := c.QueryParam("state")
	clientID := c.QueryParam("client_id")
	redirectURI := c.QueryParam("redirect_uri")
	scope := c.QueryParam("scope")

	if responseType != "code" {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "Invalid response type",
		})
	}
	if state == "" {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "The state parameter is mandatory",
		})
	}
	if clientID == "" {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "The client_id parameter is mandatory",
		})
	}
	if redirectURI == "" {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "The redirect_uri parameter is mandatory",
		})
	}
	if scope == "" {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "The scope parameter is mandatory",
		})
	}

	var client Client
	if err := couchdb.GetDoc(instance, ClientDocType, clientID, &client); err != nil {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "The client must be registered",
		})
	}
	if !client.AcceptRedirectURI(c.QueryParam("redirect_uri")) {
		return c.Render(http.StatusBadRequest, "error.html", echo.Map{
			"Error": "The redirect_uri parameter doesn't match the registered ones",
		})
	}

	if !IsLoggedIn(c) {
		redirect := url.Values{
			"redirect": {c.Request().URL.String()},
		}
		u := url.URL{
			Scheme:   "https",
			Host:     instance.Domain,
			Path:     "/auth/login",
			RawQuery: redirect.Encode(),
		}
		return c.Redirect(http.StatusSeeOther, u.String())
	}

	// TODO Trust On First Use
	// TODO CSRF token

	permissions := strings.Split(scope, " ")
	client.ClientID = client.CouchID
	return c.Render(http.StatusOK, "authorize.html", echo.Map{
		"Client":       client,
		"ResponseType": responseType,
		"State":        state,
		"RedirectURI":  redirectURI,
		"Scope":        scope,
		"Permissions":  permissions,
	})
}

// IsLoggedIn returns true if the context has a valid session cookie.
func IsLoggedIn(c echo.Context) bool {
	_, err := GetSession(c)
	return err == nil
}

// Routes sets the routing for the status service
func Routes(router *echo.Group) {
	router.POST("/register", register)

	router.GET("/auth/login", loginForm)
	router.POST("/auth/login", login)
	router.DELETE("/auth/login", logout)

	router.POST("/auth/register", registerClient)
	router.GET("/auth/authorize", authorizeForm)
}

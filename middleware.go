// Package keystone provides a go http middleware for authentication incoming
// http request against Openstack Keystone. It it modelled after the original
// keystone middleware:
// http://docs.openstack.org/developer/keystonemiddleware/middlewarearchitecture.html
//
// The middleware authenticates incoming requests by validating the `X-Auth-Token` header
// and adding additional headers to the incoming request containing the validation result.
// The final authentication/authorization decision is delegated to subsequent http handlers.
package keystone

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type authHandler struct {
	identityEndpoint string
	handler          http.Handler
	client           *http.Client
	userAgent        string
	tokenCache       Cache
}

// Cache provides the interface for cache implmentations.
// A simple in-memory cache implementation satisfying the Cache interface
// is provided by github.com/pmylund/go-cache.
type Cache interface {
	Set(k string, x interface{}, ttl time.Duration)
	Get(k string) (interface{}, bool)
}

//Handler returns a new keystone http  middleware.
//The endpoint should point to a keystone v3 url, e.g http://some.where:5000/v3.
//The cache is optional and should be set to nil to disable token caching.
func Handler(h http.Handler, endpoint string, cache Cache) http.Handler {
	return &authHandler{
		handler:          h,
		identityEndpoint: endpoint,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		userAgent:  "go-keystone-middleware/1.0",
		tokenCache: cache,
	}
}

func (h *authHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	filterIncomingHeaders(req)
	req.Header.Set("X-Identity-Status", "Invalid")
	defer h.handler.ServeHTTP(w, req)
	authToken := req.Header.Get("X-Auth-Token")
	if authToken == "" {
		return
	}

	var context *token
	//lookup token in cache
	if h.tokenCache != nil {
		if val, ok := h.tokenCache.Get(authToken); ok {
			cachedToken := val.(token)
			context = &cachedToken
		}
	}
	if context == nil {
		var err error
		context, err = h.validate(authToken)
		if err != nil {
			//ToDo: How to handle logging, printing to stdout isn't the best thing
			fmt.Println("Failed to validate token. ", err)
			return
		}
	}
	if h.tokenCache != nil {
		h.tokenCache.Set(authToken, *context, 5*time.Minute)
	}

	req.Header.Set("X-Identity-Status", "Confirmed")
	for k, v := range context.Headers() {
		req.Header.Set(k, v)
	}
}

type domain struct {
	ID      string
	Name    string
	Enabled bool
}

type project struct {
	ID       string
	DomainID string `json:"domain_id"`
	Name     string
	Enabled  bool
	Domain   *domain
}

type token struct {
	ExpiresAt string `json:"expires_at"`
	IssuedAt  string `json:"issued_at"`
	User      struct {
		ID       string
		Name     string
		Email    string
		Enabled  bool
		DomainID string `json:"domain_id"`
		Domain   struct {
			ID   string
			Name string
		}
	}
	Project *project
	Domain  *domain
	Roles   *[]struct {
		ID   string
		Name string
	}
}

type authResponse struct {
	Error *struct {
		Code    int
		Message string
		Title   string
	}
	Token *token
}

func (t token) Headers() map[string]string {
	headers := make(map[string]string)
	headers["X-User-Id"] = t.User.ID
	headers["X-User-Domain-Id"] = t.User.DomainID
	headers["X-User-Domain-Name"] = t.User.Domain.Name

	if project := t.Project; project != nil {
		headers["X-Project-Name"] = project.Name
		headers["X-Project-Id"] = project.ID
		headers["X-Project-Domain-Name"] = project.Domain.Name
		headers["X-Project-Domain-Id"] = project.DomainID

	}

	if domain := t.Domain; domain != nil {
		headers["X-Domain-Id"] = domain.ID
		headers["X-Domain-Name"] = domain.Name
	}

	if roles := t.Roles; roles != nil {
		roleNames := []string{}
		for _, role := range *t.Roles {
			roleNames = append(roleNames, role.Name)
		}
		headers["X-Roles"] = strings.Join(roleNames, ",")

	}

	return headers
}

func (h *authHandler) validate(token string) (*token, error) {

	req, err := http.NewRequest("GET", h.identityEndpoint+"/auth/tokens?nocatalog", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("X-Subject-Token", token)
	req.Header.Set("User-Agent", h.userAgent)

	r, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	var resp authResponse
	if err = json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return nil, err
	}

	if e := resp.Error; e != nil {
		return nil, fmt.Errorf("%s : %s", r.Status, e.Message)
	}
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", r.Status)
	}
	if resp.Token == nil {
		return nil, errors.New("Response didn't contain token context")
	}

	return resp.Token, nil
}

func filterIncomingHeaders(req *http.Request) {
	req.Header.Del("X-Identity-Status")
	req.Header.Del("X-Service-Identity-Status")

	req.Header.Del("X-Domain-Id")
	req.Header.Del("X-Service-Domain-Id")

	req.Header.Del("X-Domain-Name")
	req.Header.Del("X-Service-Domain-Name")

	req.Header.Del("X-Project-Id")
	req.Header.Del("X-Service-Project-Id")

	req.Header.Del("X-Project-Name")
	req.Header.Del("X-Service-Project-Name")

	req.Header.Del("X-Project-Domain-Id")
	req.Header.Del("X-Service-Project-Domain-Id")

	req.Header.Del("X-Project-Domain-Name")
	req.Header.Del("X-Service-Project-Domain-Name")

	req.Header.Del("X-User-Id")
	req.Header.Del("X-Service-User-Id")

	req.Header.Del("X-User-Name")
	req.Header.Del("X-Service-User-Name")

	req.Header.Del("X-User-Domain-Id")
	req.Header.Del("X-Service-User-Domain-Id")

	req.Header.Del("X-User-Domain-Name")
	req.Header.Del("X-Service-User-Domain-Name")

	req.Header.Del("X-Roles")
	req.Header.Del("X-Service-Roles")

	req.Header.Del("X-Servie-Catalog")

	//deprecated Headers
	req.Header.Del("X-Tenant-Id")
	req.Header.Del("X-Tenant")
	req.Header.Del("X-User")
	req.Header.Del("X-Role")
}

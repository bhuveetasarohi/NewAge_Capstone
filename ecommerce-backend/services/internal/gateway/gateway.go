// Package gateway is the single public entry point. It validates tokens on protected
// routes, forwards requests to the owning service, and serves the web UI.
package gateway

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"ecommerce-backend/services/internal/common"
)

// Upstreams holds the base URL of each backend service.
type Upstreams struct {
	User      string
	Product   string
	Inventory string
	Order     string
}

type route struct {
	prefix string
	proxy  *httputil.ReverseProxy
}

// Gateway routes /api/* to backend services and serves the frontend for everything else.
type Gateway struct {
	secret []byte
	routes []route
	static http.Handler
}

// New builds a gateway. frontendDir is the folder holding index.html, app.js and styles.css.
func New(secret []byte, frontendDir string, up Upstreams) (*Gateway, error) {
	if info, err := os.Stat(frontendDir); err != nil || !info.IsDir() {
		log.Printf("WARNING: frontend directory %q not found; the web UI will not load", frontendDir)
	}

	table := []struct {
		prefixes []string
		target   string
	}{
		{[]string{"/api/auth", "/api/users", "/api/notifications"}, up.User},
		{[]string{"/api/products", "/api/categories", "/api/recommendations"}, up.Product},
		{[]string{"/api/inventory"}, up.Inventory},
		{[]string{"/api/cart", "/api/checkout", "/api/orders"}, up.Order},
	}

	g := &Gateway{secret: secret, static: http.FileServer(http.Dir(frontendDir))}
	for _, entry := range table {
		target, err := url.Parse(entry.target)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream %q: %w", entry.target, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[gateway] upstream %s unavailable for %s: %v", target, r.URL.Path, err)
			common.WriteError(w, http.StatusBadGateway, "service temporarily unavailable")
		}
		for _, p := range entry.prefixes {
			g.routes = append(g.routes, route{prefix: p, proxy: proxy})
		}
	}
	return g, nil
}

// ServeHTTP implements http.Handler.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/health":
		common.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "gateway"})

	case path == "/api" || strings.HasPrefix(path, "/api/"):
		rt := g.match(path)
		if rt == nil {
			common.WriteError(w, http.StatusNotFound, "no such endpoint")
			return
		}
		if !isPublic(r.Method, path) {
			if _, err := common.VerifyToken(g.secret, common.BearerToken(r)); err != nil {
				common.WriteError(w, http.StatusUnauthorized, "authentication required")
				return
			}
		}
		rt.proxy.ServeHTTP(w, r)

	default:
		g.static.ServeHTTP(w, r)
	}
}

func (g *Gateway) match(path string) *route {
	for i := range g.routes {
		if hasPathPrefix(path, g.routes[i].prefix) {
			return &g.routes[i]
		}
	}
	return nil
}

// isPublic lists the routes that do not need a token: sign-in and sign-up, plus
// read-only browsing of products, categories and stock levels.
func isPublic(method, path string) bool {
	if hasPathPrefix(path, "/api/auth") {
		return true
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	return hasPathPrefix(path, "/api/products") ||
		hasPathPrefix(path, "/api/categories") ||
		hasPathPrefix(path, "/api/inventory")
}

// hasPathPrefix matches whole path segments, so /api/cart does not match /api/cartfoo.
func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

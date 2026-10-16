package wiring

import (
	"errors"
	"net/http"

	"github.com/gperezmz/wakewire/internal/account/adapters/configstore"
	"github.com/gperezmz/wakewire/internal/account/adapters/credfile"
	"github.com/gperezmz/wakewire/internal/account/adapters/ghcli"
	"github.com/gperezmz/wakewire/internal/account/adapters/idcache"
	"github.com/gperezmz/wakewire/internal/account/adapters/services"
	"github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
)

// AccountOptions are the seams a test replaces; the zero value is the real world.
type AccountOptions struct {
	GitHubAPI string                   // the GitHub API root, api.github.com by default
	Origin    func(host string) string // scheme and host of a GitLab or Jira host, https by default
	Transport http.RoundTripper        // under the read-only client
	GHBinary  string                   // the gh command
}

// NewAccounts builds the account service over the real files, services and gh.
func NewAccounts(paths home.Paths, getenv func(string) string) (*application.Service, error) {
	return NewAccountsWith(paths, getenv, AccountOptions{})
}

// NewAccountsWith builds the account service with some seams replaced.
func NewAccountsWith(paths home.Paths, getenv func(string) string, o AccountOptions) (*application.Service, error) {
	if paths.Home == "" {
		return nil, errors.New("no home folder: resolve the paths first")
	}
	ep := services.DefaultEndpoints()
	if o.GitHubAPI != "" {
		ep.GitHub = o.GitHubAPI
	}
	if o.Origin != nil {
		ep.Origin = o.Origin
	}
	return application.NewService(application.Ports{
		Config: configstore.New(paths),
		Vault:  credfile.New(paths),
		GH:     ghcli.CLI{Binary: o.GHBinary},
		Prober: services.New(httpread.New("wakewire", o.Transport), ep),
		Cache:  idcache.New(paths.Cache),
		Getenv: getenv,
	}), nil
}

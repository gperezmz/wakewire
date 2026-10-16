// Package accounts is the watch context's way into the account context:
// an anticorruption layer over its published service, so that no account
// type and no token reaches the watch context's use cases.
package accounts

import (
	"context"
	"fmt"

	account "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Accounts answers the watch context from the account service.
type Accounts struct{ Service *account.Service }

// ServiceName is how a person reads a service's kind.
func ServiceName(k account.ServiceKind) string {
	switch k {
	case account.GitHub:
		return "GitHub"
	case account.GitLab:
		return "GitLab"
	case account.Jira:
		return "Jira"
	}
	return string(k)
}

func (a Accounts) identify(ctx context.Context, c account.Credential) (application.AccountIdentity, error) {
	id, err := a.Service.WhoAmI(ctx, c)
	if err != nil {
		return application.AccountIdentity{}, err
	}
	display := id.Login
	if c.Service == account.Jira && id.Name != "" {
		display = id.Name
	}
	return application.AccountIdentity{Account: c.Account, Service: ServiceName(c.Service), Identity: id.Login, Display: display}, nil
}

// ForForge names the account reading a repository and whose it is.
func (a Accounts) ForForge(ctx context.Context, repo domain.Repo) (application.AccountIdentity, error) {
	c, err := a.Service.ForRepo(repo.Host, repo.Owner())
	if err != nil {
		return application.AccountIdentity{}, err
	}
	if c.Service == account.Jira {
		return application.AccountIdentity{}, domain.Invalid(fmt.Sprintf("%s is a Jira site, not a forge", repo.Host),
			"pass --repo with a GitHub or GitLab repository")
	}
	return a.identify(ctx, c)
}

// ForJira names the account reading a Jira site and whose it is.
func (a Accounts) ForJira(ctx context.Context, site string) (application.AccountIdentity, error) {
	c, err := a.Service.ForSite(site)
	if err != nil {
		return application.AccountIdentity{}, err
	}
	return a.identify(ctx, c)
}

// Describe answers where an account's token comes from and whether it can
// write, by checking it against its service.
func (a Accounts) Describe(ctx context.Context, name string) (application.AccountInfo, error) {
	c, err := a.Credential(name)
	if err != nil {
		return application.AccountInfo{}, err
	}
	info := application.AccountInfo{TokenSource: c.TokenSource}
	p, err := a.Service.Check(ctx, c, nil)
	if err == nil && p.CanWriteKnown {
		w := p.CanWrite
		info.CanWrite = &w
	}
	return info, nil
}

// Credential answers an account's credential by its name, for the sources.
func (a Accounts) Credential(name string) (account.Credential, error) {
	views, err := a.Service.List()
	if err != nil {
		return account.Credential{}, err
	}
	for _, v := range views {
		if v.Name != name {
			continue
		}
		if v.Service == account.Jira {
			return a.Service.ForSite(v.Host)
		}
		if len(v.Owners) == 0 {
			break
		}
		return a.Service.ForRepo(v.Host, v.Owners[0])
	}
	return account.Credential{}, domain.Refuse("no_account", fmt.Sprintf("no account is named %q", name),
		"`wakewire account list` shows the accounts; `wakewire register` again picks the right one")
}

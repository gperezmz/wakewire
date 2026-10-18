package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// Source reads one project on a GitLab instance.
type Source struct {
	HTTP  *httpread.Client
	Base  string // the instance's origin, such as https://gitlab.com
	Token string
	Repo  domain.Repo
	Name  string // the account
	Pages int    // pages read per item's list; 10 by default
}

func (s *Source) Service() string { return "GitLab" }
func (s *Source) Account() string { return s.Name }

// list is one list request's last answer.
type list struct {
	ETag string   `json:"etag"`
	Refs []string `json:"refs"`
}

// state is what the source keeps between polls.
type state struct {
	Lists map[string]list  `json:"lists"`
	Users map[string]int64 `json:"users"` // username: the numeric id the list filters take
}

func newState() state { return state{Lists: map[string]list{}, Users: map[string]int64{}} }

// poll is one Poll's working state.
type poll struct {
	s       *Source
	c       *client
	in      application.PollInput
	st      state
	project string // the project's API path

	approvers map[string][]approver // by merge request, read once per poll
	owner     *account              // the token's owner, read when commits need it
}

// approver is one user who approved a merge request.
type approver struct {
	name string
	at   time.Time // zero when the service does not say
}

// Poll lists the watched sets, diffs each item's fields against its
// snapshot, and reads the activity of every item that moved.
func (s *Source) Poll(ctx context.Context, in application.PollInput) (application.PollOutput, error) {
	base := s.Base
	if base == "" {
		base = "https://" + s.Repo.Host
	}
	p := &poll{s: s, c: &client{http: s.HTTP, base: base, token: s.Token}, in: in, st: newState(),
		project: apiRoot + "/projects/" + url.PathEscape(s.Repo.Path), approvers: map[string][]approver{}}
	if len(in.State) > 0 {
		if err := json.Unmarshal(in.State, &p.st); err != nil || p.st.Lists == nil || p.st.Users == nil {
			p.st = newState()
		}
	}
	uid, err := p.userID(ctx, in.Rules.Identity)
	if err != nil {
		return application.PollOutput{}, err
	}

	// The three lists. Without the tracker's issues, merge requests only.
	type request struct {
		path   string
		filter url.Values
		kind   domain.ItemKind
	}
	id := strconv.FormatInt(uid, 10)
	requests := []request{{p.project + "/merge_requests", url.Values{"author_id": {id}}, domain.ItemPull}}
	if in.IncludeIssues {
		requests = append([]request{
			{p.project + "/issues", url.Values{"assignee_id": {id}}, domain.ItemIssue},
			{p.project + "/issues", url.Values{"author_id": {id}, "assignee_id": {"None"}}, domain.ItemIssue},
		}, requests...)
	}
	seen := map[string]domain.Item{}
	for _, r := range requests {
		q := url.Values{"state": {"all"}, "order_by": {"updated_at"}, "sort": {"desc"}, "per_page": {"100"}}
		maps.Copy(q, r.filter)
		body, err := p.list(ctx, r.path, q)
		if err != nil {
			return application.PollOutput{}, err
		}
		if body == nil {
			continue
		}
		var entries []entry
		if err := json.Unmarshal(body, &entries); err != nil {
			return application.PollOutput{}, fmt.Errorf("GitLab's %s: %w", lastSegment(r.path), err)
		}
		var refs []string
		for _, e := range entries {
			it := e.item(r.kind)
			refs = append(refs, it.Ref)
			seen[it.Ref] = it
		}
		key := listKey(r.path, q)
		l := p.st.Lists[key]
		l.Refs = refs
		p.st.Lists[key] = l
	}

	if err := p.refreshOpenMerge(ctx, seen); err != nil {
		return application.PollOutput{}, err
	}

	listed := map[string]bool{}
	for _, l := range p.st.Lists {
		for _, r := range l.Refs {
			listed[r] = true
		}
	}

	var obs []domain.Observation
	for ref, it := range seen {
		snap, had := in.Snapshots[ref]
		if !had && !in.Rules.InSet(it) {
			continue
		}
		moved := !had || !sameItem(snap.Item, it)
		if !moved && it.Kind == domain.ItemPull && it.State == "open" {
			// An approval moves nothing the lists or the merge request
			// show, so every open merge request's approvals are read.
			if moved, err = p.newApprover(ctx, ref, snap); err != nil {
				return application.PollOutput{}, err
			}
		}
		if !moved {
			continue
		}
		o, err := p.observe(ctx, it, snap, had)
		if err != nil {
			return application.PollOutput{}, err
		}
		obs = append(obs, o)
	}
	// A watched item missing from every list is read alone, once.
	for ref, snap := range in.Snapshots {
		if listed[ref] || snap.OffList {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		it, ok, err := p.one(ctx, snap.Item)
		if err != nil {
			return application.PollOutput{}, err
		}
		var o domain.Observation
		if !ok || sameItem(snap.Item, it) {
			o = domain.Observation{Item: snap.Item}
		} else if o, err = p.observe(ctx, it, snap, true); err != nil {
			return application.PollOutput{}, err
		}
		o.OffList = true
		obs = append(obs, o)
	}

	at := p.c.at
	if at.IsZero() {
		at = time.Now().UTC()
	}
	for i := range obs {
		obs[i].At = at
	}
	slices.SortFunc(obs, func(a, b domain.Observation) int { return strings.Compare(a.Item.Ref, b.Item.Ref) })
	raw, err := json.Marshal(p.st)
	if err != nil {
		return application.PollOutput{}, err
	}
	return application.PollOutput{Observations: obs, State: raw, Rate: p.c.rate, At: at}, nil
}

func lastSegment(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func listKey(path string, q url.Values) string { return path + "?" + q.Encode() }

func sameItem(a, b domain.Item) bool { return reflect.DeepEqual(a, b) }

// userID answers the numeric id of a username, which the list filters
// take. It is asked once and kept in the source's state.
func (p *poll) userID(ctx context.Context, username string) (int64, error) {
	if id, ok := p.st.Users[username]; ok {
		return id, nil
	}
	resp, err := p.c.get(ctx, apiRoot+"/users", url.Values{"username": {username}}, "")
	if err != nil {
		return 0, err
	}
	var users []user
	if resp.Status/100 == 2 {
		if err := json.Unmarshal(resp.Body, &users); err != nil {
			return 0, fmt.Errorf("GitLab's users: %w", err)
		}
	}
	for _, u := range users {
		if strings.EqualFold(u.Username, username) {
			p.st.Users[username] = u.ID
			return u.ID, nil
		}
	}
	return 0, httpread.NewError("invalid_value", fmt.Sprintf("GitLab has no user %q", username),
		"fix `user` of the account in config.toml, or `wakewire account list` to see whose it is")
}

// list sends one list request with the last ETag. It answers the body when
// the list changed, and nil when GitLab answered 304.
func (p *poll) list(ctx context.Context, path string, q url.Values) ([]byte, error) {
	key := listKey(path, q)
	l := p.st.Lists[key]
	resp, err := p.c.get(ctx, path, q, l.ETag)
	if err != nil {
		return nil, err
	}
	switch resp.Status {
	case 304:
		return nil, nil
	case 404:
		return nil, httpread.NewError("token_refused", fmt.Sprintf("GitLab answered 404 for %s: the token cannot see it", p.s.Repo),
			"check the project's name and that the account's token can read it: `wakewire doctor`")
	}
	l.ETag = resp.Header.Get("Etag")
	p.st.Lists[key] = l
	return resp.Body, nil
}

// refreshOpenMerge replaces every open merge request's list entry with a
// read of the merge request alone. GitLab caches a merge request's entry in
// the project's list until its updated_at moves, which a push or a comment
// does not do: the list keeps the old head and comment count, and only a
// read of the merge request itself shows the current ones.
func (p *poll) refreshOpenMerge(ctx context.Context, seen map[string]domain.Item) error {
	open := map[string]bool{}
	for ref, it := range seen {
		if it.Kind == domain.ItemPull && it.State == "open" {
			open[ref] = true
		}
	}
	for ref, snap := range p.in.Snapshots {
		if snap.Item.Kind == domain.ItemPull && snap.Item.State == "open" {
			open[ref] = true
		}
	}
	refs := make([]string, 0, len(open))
	for ref := range open {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	for _, ref := range refs {
		it, ok, err := p.read(ctx, ref, domain.ItemPull)
		if err != nil {
			return err
		}
		if ok {
			seen[ref] = it
		}
	}
	return nil
}

// read reads one item alone.
func (p *poll) read(ctx context.Context, ref string, kind domain.ItemKind) (domain.Item, bool, error) {
	resp, err := p.c.get(ctx, p.itemPath(ref), nil, "")
	if err != nil || resp.Status/100 != 2 {
		return domain.Item{}, false, err
	}
	var e entry
	if err := json.Unmarshal(resp.Body, &e); err != nil {
		return domain.Item{}, false, fmt.Errorf("GitLab's %s: %w", ref, err)
	}
	return e.item(kind), true, nil
}

func (p *poll) one(ctx context.Context, was domain.Item) (domain.Item, bool, error) {
	return p.read(ctx, was.Ref, was.Kind)
}

// itemPath is an item's API path, from its reference: #12 or !41.
func (p *poll) itemPath(ref string) string {
	if n, ok := strings.CutPrefix(ref, "!"); ok {
		return p.project + "/merge_requests/" + n
	}
	return p.project + "/issues/" + strings.TrimPrefix(ref, "#")
}

// approvalsOf answers who approved a merge request, read once per poll.
func (p *poll) approvalsOf(ctx context.Context, ref string) ([]approver, error) {
	if a, ok := p.approvers[ref]; ok {
		return a, nil
	}
	resp, err := p.c.get(ctx, p.itemPath(ref)+"/approvals", nil, "")
	if err != nil {
		return nil, err
	}
	var out []approver
	if resp.Status/100 == 2 {
		var doc approvals
		if err := json.Unmarshal(resp.Body, &doc); err != nil {
			return nil, fmt.Errorf("GitLab's approvals of %s: %w", ref, err)
		}
		for _, a := range doc.ApprovedBy {
			out = append(out, approver{name: a.User.Username, at: parseTime(a.ApprovedAt)})
		}
	}
	p.approvers[ref] = out
	return out, nil
}

// newApprover says whether a merge request has an approval the snapshot
// has not recorded.
func (p *poll) newApprover(ctx context.Context, ref string, snap domain.Snapshot) (bool, error) {
	if p.in.FirstPoll {
		return false, nil
	}
	as, err := p.approvalsOf(ctx, ref)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(as, func(a approver) bool { return !slices.Contains(snap.Seen, "ap:"+a.name) }), nil
}

// observe reads an item's events, notes and, for a merge request, its
// approvals and new commits.
func (p *poll) observe(ctx context.Context, it domain.Item, snap domain.Snapshot, had bool) (domain.Observation, error) {
	o := domain.Observation{Item: it}
	if p.in.FirstPoll && !had {
		return o, nil // what is there now is no change: nothing to read
	}
	pages := p.s.Pages
	if pages == 0 {
		pages = 10
	}
	base := p.itemPath(it.Ref)
	q := func() url.Values { return url.Values{"per_page": {"100"}} }

	labels, err := getAll[labelEvent](ctx, p.c, base+"/resource_label_events", q(), pages)
	if err != nil {
		return o, err
	}
	for _, e := range labels {
		if a, ok := e.activity(); ok {
			o.Activities = append(o.Activities, a)
		}
	}
	states, err := getAll[stateEvent](ctx, p.c, base+"/resource_state_events", q(), pages)
	if err != nil {
		return o, err
	}
	for _, e := range states {
		if a, ok := e.activity(); ok {
			o.Activities = append(o.Activities, a)
		}
	}
	nq := q()
	nq.Set("order_by", "updated_at")
	nq.Set("sort", "desc")
	notes, err := getAll[note](ctx, p.c, base+"/notes", nq, pages)
	if err != nil {
		return o, err
	}
	for _, n := range notes {
		if a, ok := n.activity(it.URL); ok {
			o.Activities = append(o.Activities, a)
		}
	}
	if it.Kind != domain.ItemPull {
		return o, nil
	}

	as, err := p.approvalsOf(ctx, it.Ref)
	if err != nil {
		return o, err
	}
	for _, a := range as {
		at := a.at
		if at.IsZero() {
			at = approvedAt(notes, a.name)
		}
		if at.IsZero() {
			at = p.c.at
		}
		o.Activities = append(o.Activities, domain.Activity{ID: "ap:" + a.name, Kind: domain.KindReview, State: "approved",
			By: a.name, At: at, URL: it.URL})
	}

	if had && it.Head != "" && it.Head != snap.Item.Head {
		commits, err := getAll[commit](ctx, p.c, base+"/commits", q(), 3)
		if err != nil {
			return o, err
		}
		slices.Reverse(commits) // GitLab lists the newest first
		// The new commits are those after the head last seen; after a force
		// push that head is gone, and every commit counts.
		from := 0
		for i, cm := range commits {
			if cm.ID == snap.Item.Head {
				from = i + 1
			}
		}
		for _, cm := range commits[from:] {
			author, err := p.commitAuthor(ctx, cm.AuthorEmail)
			if err != nil {
				return o, err
			}
			o.Activities = append(o.Activities, domain.Activity{ID: "commit:" + cm.ID, Kind: domain.KindPushed,
				At: p.c.at, Commit: &domain.Commit{ID: cm.ID, Author: author}})
		}
	}
	return o, nil
}

// approvedAt is when username approved, from the system note GitLab wrote
// about it; the newest, since an approval can be withdrawn and given again.
func approvedAt(notes []note, username string) time.Time {
	var at time.Time
	for _, n := range notes {
		if n.System && n.Body == approvedNote && n.Author.Username == username {
			if t := parseTime(n.CreatedAt); t.After(at) {
				at = t
			}
		}
	}
	return at
}

// commitAuthor names a commit's author: a commit carries an email, not a
// username, so the identity's own commits are told by the email of the
// token's owner. Anyone else is named by email.
func (p *poll) commitAuthor(ctx context.Context, email string) (string, error) {
	if p.owner == nil {
		resp, err := p.c.get(ctx, apiRoot+"/user", nil, "")
		if err != nil {
			return "", err
		}
		p.owner = &account{}
		if resp.Status/100 == 2 {
			if err := json.Unmarshal(resp.Body, p.owner); err != nil {
				return "", fmt.Errorf("GitLab's user: %w", err)
			}
		}
	}
	if strings.EqualFold(p.owner.Username, p.in.Rules.Identity) && p.owner.hasEmail(email) {
		return p.in.Rules.Identity, nil
	}
	return email, nil
}

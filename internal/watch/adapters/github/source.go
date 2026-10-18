package github

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

// Source reads one repository on GitHub.
type Source struct {
	HTTP    *httpread.Client
	Base    string // API by default
	Token   string
	Repo    domain.Repo
	Name    string // the account
	Pages   int    // timeline pages read per item; 10 by default
	Clock   func() time.Time
	Timeout time.Duration
}

func (s *Source) Service() string { return "GitHub" }
func (s *Source) Account() string { return s.Name }

// list is one list request's last answer.
type list struct {
	ETag string   `json:"etag"`
	Refs []string `json:"refs"`
}

// state is what the source keeps between polls.
type state struct {
	Lists map[string]list   `json:"lists"`
	Heads map[string]string `json:"heads"` // pull request number: head commit
}

// Poll lists the watched sets, diffs each item's fields against its
// snapshot, and reads the timeline of every item that moved.
func (s *Source) Poll(ctx context.Context, in application.PollInput) (application.PollOutput, error) {
	base := s.Base
	if base == "" {
		base = API
	}
	c := &client{http: s.HTTP, base: base, token: s.Token}
	st := state{Lists: map[string]list{}, Heads: map[string]string{}}
	if len(in.State) > 0 {
		if err := json.Unmarshal(in.State, &st); err != nil {
			st = state{Lists: map[string]list{}, Heads: map[string]string{}}
		}
	}
	repoPath := "/repos/" + s.Repo.Path
	login := in.Rules.Identity

	// Request 3 first, so the issues lists can carry each pull request's head.
	pullsQ := url.Values{"state": {"open"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"100"}}
	headsMoved := false
	if resp, err := s.list(ctx, c, &st, repoPath+"/pulls", pullsQ); err != nil {
		return application.PollOutput{}, err
	} else if resp != nil {
		var pulls []pull
		if err := json.Unmarshal(resp, &pulls); err != nil {
			return application.PollOutput{}, fmt.Errorf("GitHub's pull requests: %w", err)
		}
		heads := map[string]string{}
		for _, p := range pulls {
			heads[strconv.Itoa(p.Number)] = p.Head.SHA
		}
		for n, sha := range st.Heads {
			if _, open := heads[n]; !open {
				heads[n] = sha // a closed pull request keeps its last head
			}
		}
		headsMoved = !reflect.DeepEqual(heads, st.Heads)
		st.Heads = heads
	}

	seen := map[string]domain.Item{}
	for _, filter := range []url.Values{
		{"assignee": {login}},
		{"creator": {login}},
	} {
		q := url.Values{"state": {"all"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"100"}}
		maps.Copy(q, filter)
		if !in.IncludeIssues && filter.Has("assignee") {
			continue // specs live on another tracker; pull requests are found by their author
		}
		body, err := s.list(ctx, c, &st, repoPath+"/issues", q)
		if err != nil {
			return application.PollOutput{}, err
		}
		if body == nil {
			continue
		}
		var issues []issue
		if err := json.Unmarshal(body, &issues); err != nil {
			return application.PollOutput{}, fmt.Errorf("GitHub's issues: %w", err)
		}
		var refs []string
		for _, i := range issues {
			it := i.item(st.Heads[strconv.Itoa(i.Number)])
			if it.Kind == domain.ItemIssue && !in.IncludeIssues {
				continue
			}
			refs = append(refs, it.Ref)
			seen[it.Ref] = it
		}
		key := listKey(repoPath+"/issues", q)
		l := st.Lists[key]
		l.Refs = refs
		st.Lists[key] = l
	}

	// Pull requests whose head moved, when no issues list was new.
	if headsMoved {
		for ref, snap := range in.Snapshots {
			if snap.Item.Kind != domain.ItemPull {
				continue
			}
			n := strings.TrimPrefix(ref, "PR #")
			if h := st.Heads[n]; h != "" && h != snap.Item.Head {
				it := snap.Item
				if cur, ok := seen[ref]; ok {
					it = cur
				}
				it.Head = h
				seen[ref] = it
			}
		}
	}

	listed := map[string]bool{}
	for _, l := range st.Lists {
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
		if had && sameItem(snap.Item, it) {
			continue
		}
		o, err := s.observe(ctx, c, in, it, snap, had)
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
		it, ok, err := s.one(ctx, c, repoPath, snap.Item, st.Heads)
		if err != nil {
			return application.PollOutput{}, err
		}
		var o domain.Observation
		if !ok || sameItem(snap.Item, it) {
			o = domain.Observation{Item: snap.Item}
		} else if o, err = s.observe(ctx, c, in, it, snap, true); err != nil {
			return application.PollOutput{}, err
		}
		o.OffList = true
		obs = append(obs, o)
	}

	at := c.at
	if at.IsZero() {
		at = time.Now().UTC()
	}
	for i := range obs {
		obs[i].At = at
	}
	slices.SortFunc(obs, func(a, b domain.Observation) int { return strings.Compare(a.Item.Ref, b.Item.Ref) })
	raw, err := json.Marshal(st)
	if err != nil {
		return application.PollOutput{}, err
	}
	return application.PollOutput{Observations: obs, State: raw, Rate: c.rate, At: at}, nil
}

func listKey(path string, q url.Values) string { return path + "?" + q.Encode() }

// list sends one list request with the last ETag. It answers the body when
// the list changed, and nil when GitHub answered 304.
func (s *Source) list(ctx context.Context, c *client, st *state, path string, q url.Values) ([]byte, error) {
	key := listKey(path, q)
	l := st.Lists[key]
	resp, err := c.get(ctx, path, q, l.ETag)
	if err != nil {
		return nil, err
	}
	switch resp.Status {
	case 304:
		return nil, nil
	case 404, 410:
		return nil, httpread.NewError("token_refused", fmt.Sprintf("GitHub answered %d for %s: the token cannot see it", resp.Status, s.Repo),
			"check the repository's name and that the account's token can read it: `wakewire doctor`")
	}
	l.ETag = resp.Header.Get("Etag")
	st.Lists[key] = l
	return resp.Body, nil
}

// one reads a single item.
func (s *Source) one(ctx context.Context, c *client, repoPath string, was domain.Item, heads map[string]string) (domain.Item, bool, error) {
	n := strings.TrimPrefix(strings.TrimPrefix(was.Ref, "PR "), "#")
	resp, err := c.get(ctx, repoPath+"/issues/"+n, nil, "")
	if err != nil || resp.Status/100 != 2 {
		return domain.Item{}, false, err
	}
	var i issue
	if err := json.Unmarshal(resp.Body, &i); err != nil {
		return domain.Item{}, false, err
	}
	return i.item(heads[n]), true, nil
}

// observe reads an item's timeline, inline comments and new commits.
func (s *Source) observe(ctx context.Context, c *client, in application.PollInput, it domain.Item, snap domain.Snapshot, had bool) (domain.Observation, error) {
	o := domain.Observation{Item: it}
	if in.FirstPoll && !had {
		return o, nil // what is there now is no change: nothing to read
	}
	pages := s.Pages
	if pages == 0 {
		pages = 10
	}
	repoPath := "/repos/" + s.Repo.Path
	n := strings.TrimPrefix(strings.TrimPrefix(it.Ref, "PR "), "#")
	events, err := getLast[event](ctx, c, repoPath+"/issues/"+n+"/timeline", url.Values{"per_page": {"100"}}, pages)
	if err != nil {
		return o, err
	}
	for _, e := range events {
		if a, ok := e.activity(); ok {
			o.Activities = append(o.Activities, a)
		}
	}
	if it.Kind != domain.ItemPull {
		return o, nil
	}
	comments, err := getLast[reviewComment](ctx, c, repoPath+"/pulls/"+n+"/comments", url.Values{"per_page": {"100"}}, pages)
	if err != nil {
		return o, err
	}
	for _, rc := range comments {
		o.Activities = append(o.Activities, rc.activity())
	}
	if had && it.Head != "" && it.Head != snap.Item.Head {
		commits, err := getLast[commit](ctx, c, repoPath+"/pulls/"+n+"/commits", url.Values{"per_page": {"100"}}, 3)
		if err != nil {
			return o, err
		}
		// The new commits are those after the head last seen; after a force
		// push that head is gone, and every commit counts.
		from := 0
		for i, cm := range commits {
			if cm.SHA == snap.Item.Head {
				from = i + 1
			}
		}
		for _, cm := range commits[from:] {
			o.Activities = append(o.Activities, domain.Activity{ID: "commit:" + cm.SHA, Kind: domain.KindPushed,
				At: c.at, Commit: &domain.Commit{ID: cm.SHA, Author: cm.author()}})
		}
	}
	return o, nil
}

// sameItem compares the fields a poll diffs.
func sameItem(a, b domain.Item) bool { return reflect.DeepEqual(a, b) }

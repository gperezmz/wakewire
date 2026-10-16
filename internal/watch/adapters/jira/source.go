package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Source reads one Jira project's specs.
type Source struct {
	HTTP    *httpread.Client
	Base    string // https://<site> by default
	Site    string // the Jira Cloud site, such as acme.atlassian.net
	Project string // the project key
	Email   string
	Token   string
	Name    string // the account
	Pages   int    // pages read of each paged answer; 5 by default
}

func (s *Source) Service() string { return "Jira" }
func (s *Source) Account() string { return s.Name }

// fields are the issue fields one search asks for.
var fields = []string{"summary", "description", "labels", "assignee", "reporter", "status", "resolution", "comment", "updated"}

// reconcileMax is the most issue ids Jira takes in reconcileIssues.
const reconcileMax = 50

// state is what the source keeps between polls.
type state struct {
	IDs      map[string]string `json:"ids"`      // issue key: issue id, for reconcileIssues
	Statuses map[string]string `json:"statuses"` // status id, or "name:" and its name: status category key
}

// Poll searches the partition and the watched issues in one request, diffs
// nothing itself, and reads the changelog and comments of every issue that
// is new or whose fields moved.
func (s *Source) Poll(ctx context.Context, in application.PollInput) (application.PollOutput, error) {
	base := s.Base
	if base == "" {
		base = "https://" + s.Site
	}
	c := &client{http: s.HTTP, base: base, email: s.Email, token: s.Token}
	st := state{IDs: map[string]string{}, Statuses: map[string]string{}}
	if len(in.State) > 0 {
		if err := json.Unmarshal(in.State, &st); err != nil || st.IDs == nil || st.Statuses == nil {
			st = state{IDs: map[string]string{}, Statuses: map[string]string{}}
		}
	}

	watched := make([]string, 0, len(in.Snapshots))
	for ref, snap := range in.Snapshots {
		if !snap.OffList { // gone or hidden already: not asked about again, which Jira may answer with a 400
			watched = append(watched, ref)
		}
	}
	slices.Sort(watched)
	var reconcile []int64
	for _, k := range watched {
		if n, err := strconv.ParseInt(st.IDs[k], 10, 64); err == nil && len(reconcile) < reconcileMax {
			reconcile = append(reconcile, n)
		}
	}

	found, complete, err := s.search(ctx, c, s.jql(in.Rules, watched), reconcile)
	if err != nil {
		return application.PollOutput{}, err
	}
	seen := map[string]domain.Item{}
	for _, i := range found {
		if i.Fields.Status != nil && i.Fields.Status.ID != "" {
			st.Statuses[i.Fields.Status.ID] = i.category()
			st.Statuses["name:"+i.Fields.Status.Name] = i.category()
		}
		st.IDs[i.Key] = i.ID
		seen[i.Key] = i.item(s.Site)
	}
	for k := range st.IDs {
		if _, ok := seen[k]; !ok && !slices.Contains(watched, k) {
			delete(st.IDs, k)
		}
	}

	var obs []domain.Observation
	for ref, it := range seen {
		snap, had := in.Snapshots[ref]
		if !had && !in.Rules.InSet(it) {
			continue
		}
		if had && reflect.DeepEqual(snap.Item, it) {
			continue
		}
		obs = append(obs, domain.Observation{Item: it})
	}
	// What is there on the watch's first poll is no change: no timeline is read.
	var moved []*domain.Observation
	for i := range obs {
		if !in.FirstPoll {
			moved = append(moved, &obs[i])
		}
	}
	if err := s.readTimelines(ctx, c, st, moved, in.Snapshots); err != nil {
		return application.PollOutput{}, err
	}

	// A watched issue the search no longer returns is gone or hidden: it is
	// reported once as it was, and not asked about again.
	if complete {
		for ref, snap := range in.Snapshots {
			if _, ok := seen[ref]; ok || snap.OffList {
				continue
			}
			obs = append(obs, domain.Observation{Item: snap.Item, OffList: true})
		}
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

// jql asks for the partition's specs, and for every watched issue so that
// one leaving the partition stays in view until that is recorded.
func (s *Source) jql(r domain.Rules, watched []string) string {
	id := quote(r.Identity)
	partition := "project = " + quote(s.Project)
	if spec := r.Labels[domain.MeaningSpec]; spec != "" {
		partition += " AND labels = " + quote(spec)
	}
	partition += " AND (assignee = " + id + " OR (reporter = " + id + " AND assignee is EMPTY))"
	if len(watched) == 0 {
		return partition + " ORDER BY updated DESC"
	}
	keys := make([]string, len(watched))
	for i, k := range watched {
		keys[i] = quote(k)
	}
	return "(" + partition + ") OR issuekey in (" + strings.Join(keys, ", ") + ") ORDER BY updated DESC"
}

// quote writes a JQL string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// search runs the one search, following nextPageToken for a few pages. It
// answers false when pages ran out before the results did.
func (s *Source) search(ctx context.Context, c *client, jql string, reconcile []int64) ([]issue, bool, error) {
	pages := s.Pages
	if pages == 0 {
		pages = 5
	}
	var all []issue
	token := ""
	for i := 0; i < pages; i++ {
		body := map[string]any{"jql": jql, "fields": fields, "maxResults": 100}
		if len(reconcile) > 0 {
			body["reconcileIssues"] = reconcile
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		resp, err := c.do(ctx, "POST", "/rest/api/3/search/jql", nil, body)
		if err != nil {
			return nil, false, err
		}
		if resp.Status/100 != 2 {
			return nil, false, httpread.NewError("token_refused", fmt.Sprintf("Jira answered %d for the search on %s: the account cannot see it", resp.Status, s.Project),
				"check the site and project key, and that the account can browse the project: `wakewire doctor`")
		}
		var page searchPage
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, false, fmt.Errorf("reading the search on Jira: %w", err)
		}
		all = append(all, page.Issues...)
		token = page.NextPageToken
		if token == "" || (page.IsLast != nil && *page.IsLast) {
			return all, true, nil
		}
	}
	return all, false, nil
}

// readTimelines fills the activities of the observations: one changelog
// request for all of them, and one comment request for each issue whose
// comment total moved.
func (s *Source) readTimelines(ctx context.Context, c *client, st state, obs []*domain.Observation, snaps map[string]domain.Snapshot) error {
	if len(obs) == 0 {
		return nil
	}
	keys := make([]string, len(obs))
	for i, o := range obs {
		keys[i] = o.Item.Ref
	}
	histories, err := s.changelogs(ctx, c, keys, st.IDs)
	if err != nil {
		return err
	}
	for _, o := range obs {
		for _, h := range histories[o.Item.Ref] {
			o.Activities = append(o.Activities, h.activities(st.Statuses)...)
		}
		snap, had := snaps[o.Item.Ref]
		if had && o.Item.Comments >= 0 && o.Item.Comments == snap.Item.Comments {
			continue // no comment was written
		}
		comments, err := s.comments(ctx, c, o.Item.Ref)
		if err != nil {
			return err
		}
		for _, cm := range comments {
			o.Activities = append(o.Activities, cm.activity(s.Site, o.Item.Ref))
		}
	}
	return nil
}

// changelogs reads the histories of every issue at once, oldest first, and
// falls back to one request per issue when the bulk request fails. ids
// maps issue keys to the ids the bulk answer is keyed by.
func (s *Source) changelogs(ctx context.Context, c *client, keys []string, ids map[string]string) (map[string][]history, error) {
	byID, err := s.bulkChangelogs(ctx, c, keys)
	if err == nil {
		out := map[string][]history{}
		for _, k := range keys {
			out[k] = byID[ids[k]]
		}
		return out, nil
	}
	var out map[string][]history
	var coded interface{ Code() string }
	if errors.As(err, &coded) && (coded.Code() == "token_refused" || coded.Code() == "rate_limited") {
		return nil, err
	}
	out = map[string][]history{}
	for _, k := range keys {
		hs, err := s.issueChangelog(ctx, c, k)
		if err != nil {
			return nil, err
		}
		out[k] = hs
	}
	return out, nil
}

func (s *Source) bulkChangelogs(ctx context.Context, c *client, keys []string) (map[string][]history, error) {
	pages := s.Pages
	if pages == 0 {
		pages = 5
	}
	out := map[string][]history{}
	token := ""
	for i := 0; i < pages; i++ {
		body := map[string]any{"issueIdsOrKeys": keys}
		if token != "" {
			body["nextPageToken"] = token
		}
		resp, err := c.do(ctx, "POST", "/rest/api/3/changelog/bulkfetch", nil, body)
		if err != nil {
			return nil, err
		}
		if resp.Status/100 != 2 {
			return nil, httpread.NewError("unreachable", fmt.Sprintf("Jira answered %d to the bulk changelog", resp.Status), "`wakewire doctor`")
		}
		var page struct {
			IssueChangeLogs []struct {
				IssueID         string    `json:"issueId"`
				ChangeHistories []history `json:"changeHistories"`
			} `json:"issueChangeLogs"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("reading the bulk changelog from Jira: %w", err)
		}
		for _, l := range page.IssueChangeLogs {
			out[l.IssueID] = append(out[l.IssueID], l.ChangeHistories...)
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	return out, nil
}

// issueChangelog reads one issue's histories, oldest first.
func (s *Source) issueChangelog(ctx context.Context, c *client, key string) ([]history, error) {
	var all []history
	start, pages := 0, s.Pages
	if pages == 0 {
		pages = 5
	}
	for i := 0; i < pages; i++ {
		resp, err := c.do(ctx, "GET", "/rest/api/3/issue/"+url.PathEscape(key)+"/changelog", url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"100"}}, nil)
		if err != nil {
			return nil, err
		}
		if resp.Status/100 != 2 {
			return all, nil
		}
		var page struct {
			Values []history `json:"values"`
			IsLast *bool     `json:"isLast"`
			Total  int       `json:"total"`
		}
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("reading the changelog of %s from Jira: %w", key, err)
		}
		all = append(all, page.Values...)
		start += len(page.Values)
		if len(page.Values) == 0 || (page.IsLast != nil && *page.IsLast) || (page.IsLast == nil && start >= page.Total) {
			break
		}
	}
	return all, nil
}

// comments reads an issue's comments, oldest first.
func (s *Source) comments(ctx context.Context, c *client, key string) ([]comment, error) {
	var all []comment
	start, pages := 0, s.Pages
	if pages == 0 {
		pages = 5
	}
	for i := 0; i < pages; i++ {
		resp, err := c.do(ctx, "GET", "/rest/api/3/issue/"+url.PathEscape(key)+"/comment",
			url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"100"}, "orderBy": {"created"}}, nil)
		if err != nil {
			return nil, err
		}
		if resp.Status/100 != 2 {
			return all, nil
		}
		var page struct {
			Comments []comment `json:"comments"`
			Total    int       `json:"total"`
		}
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("reading the comments on %s from Jira: %w", key, err)
		}
		all = append(all, page.Comments...)
		start += len(page.Comments)
		if len(page.Comments) == 0 || start >= page.Total {
			break
		}
	}
	return all, nil
}

package domain

import (
	"slices"
	"strings"
	"time"
)

// HeldEdit is a title or description edit waiting for its text to hold
// still for one poll.
type HeldEdit struct {
	Before string    `json:"before"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// Snapshot is what wakewire last saw of one watched item.
type Snapshot struct {
	Item Item `json:"item"`
	// Seen are the IDs of the activities already recorded.
	Seen []string `json:"seen"`
	// Baseline: an activity at or before it was there before the watch
	// first saw the item, and is never a change.
	Baseline time.Time `json:"baseline"`
	// ObservedAt is the service's time when the item was last read.
	ObservedAt time.Time           `json:"observedAt"`
	Held       map[string]HeldEdit `json:"held,omitempty"`
	// OffList is set when the item fell off the source's lists and a read of
	// it alone showed it still in the set: it is not read alone again until
	// it shows up in a list.
	OffList bool `json:"offList,omitempty"`
}

// Observation is one item as a poll read it.
type Observation struct {
	Item Item
	// Activities are the item's timeline as read this poll; a source reads
	// them whenever the item's fields or update time moved, or the item is
	// new to the watch.
	Activities []Activity
	// At is the service's time of the read.
	At time.Time
	// OffList: the item was read alone, having fallen off the lists.
	OffList bool
}

// Diffed is what one observation comes to.
type Diffed struct {
	Changes []Change // each classified, Status and Reason set
	Next    Snapshot
	Drop    bool // the item left the sets: forget its snapshot
}

// Diff compares an observation with the item's snapshot. prev is nil for
// an item the watch has not seen; since is then the time after which its
// activities are changes, and zero on the watch's first poll, when nothing
// already there is a change.
func Diff(r Rules, prev *Snapshot, obs Observation, since time.Time) Diffed {
	if prev == nil {
		return diffNew(r, obs, since)
	}
	d := &differ{r: r, prev: prev, obs: obs, consumed: map[string]bool{}}
	d.run()
	next := Snapshot{
		Item:       obs.Item,
		Seen:       mergeSeen(prev.Seen, obs.Activities),
		Baseline:   prev.Baseline,
		ObservedAt: obs.At,
		Held:       d.held,
		OffList:    obs.OffList,
	}
	drop := !r.InSet(obs.Item)
	if drop {
		d.flushHeld()
		next.Held = nil
	}
	return Diffed{Changes: classifyAll(d.changes, obs.Item.Kind, r), Next: next, Drop: drop}
}

func diffNew(r Rules, obs Observation, since time.Time) Diffed {
	next := Snapshot{Item: obs.Item, Seen: mergeSeen(nil, obs.Activities), ObservedAt: obs.At}
	if since.IsZero() {
		next.Baseline = obs.At
		return Diffed{Next: next, Drop: !r.InSet(obs.Item)}
	}
	next.Baseline = since
	var out []Change
	var commits []Commit
	for _, a := range obs.Activities {
		if !a.At.After(since) {
			continue
		}
		if a.Kind == KindPushed {
			if a.Commit != nil {
				commits = append(commits, *a.Commit)
			}
			continue
		}
		if a.Kind == KindEdited || a.Kind == KindReassigned {
			continue // an item new to the watch has no earlier text or assignees to compare
		}
		out = append(out, fromActivity(obs.Item, a))
	}
	if len(commits) > 0 {
		out = append(out, Change{Item: obs.Item.Ref, URL: obs.Item.URL, Kind: KindPushed, At: obs.At, Head: obs.Item.Head, Commits: commits})
	}
	// A spec that came into the partition with nothing in its timeline saying
	// so, as a Jira issue created with its labels is: it gained the spec label
	// as far as the watch can tell.
	if obs.Item.Kind == ItemIssue && r.InSet(obs.Item) && !entered(out, r) {
		out = append(out, Change{Item: obs.Item.Ref, URL: obs.Item.URL, Kind: KindLabelAdded, Label: r.Labels[MeaningSpec],
			At: obs.At})
	}
	return Diffed{Changes: classifyAll(out, obs.Item.Kind, r), Next: next, Drop: !r.InSet(obs.Item)}
}

// entered says whether changes show a spec entering the partition: the
// spec label added, or the spec given to the identity.
func entered(cs []Change, r Rules) bool {
	for _, c := range cs {
		if c.Kind == KindLabelAdded && r.Labels.Is(c.Label, MeaningSpec) {
			return true
		}
		if c.Kind == KindReassigned {
			return true
		}
	}
	return false
}

// Settle reports the held edits of an item that was not read this poll,
// since its text cannot have moved.
func Settle(r Rules, s Snapshot) ([]Change, Snapshot) {
	if len(s.Held) == 0 {
		return nil, s
	}
	d := &differ{r: r, prev: &s, obs: Observation{Item: s.Item, At: s.ObservedAt}, held: s.Held}
	d.flushHeld()
	s.Held = nil
	return classifyAll(d.changes, s.Item.Kind, r), s
}

type differ struct {
	r        Rules
	prev     *Snapshot
	obs      Observation
	consumed map[string]bool
	changes  []Change
	held     map[string]HeldEdit
}

func (d *differ) fresh(a Activity) bool {
	return !slices.Contains(d.prev.Seen, a.ID) && a.At.After(d.prev.Baseline) && !d.consumed[a.ID]
}

// claim finds the newest fresh activity matching ok and answers who made
// it and when; the observation's time where none matches. Every fresh
// activity matching ok is consumed with it: a service may record one change
// more than once, as GitLab records a spec closed by a merge.
func (d *differ) claim(ok func(Activity) bool) (string, time.Time) {
	idx := -1
	for i, a := range d.obs.Activities {
		if d.fresh(a) && ok(a) {
			if idx < 0 || a.At.After(d.obs.Activities[idx].At) {
				idx = i
			}
		}
	}
	if idx < 0 {
		return "", d.obs.At
	}
	a := d.obs.Activities[idx]
	for _, b := range d.obs.Activities {
		if d.fresh(b) && ok(b) {
			d.consumed[b.ID] = true
		}
	}
	return a.By, a.At
}

func (d *differ) add(c Change) {
	c.Item, c.URL = d.obs.Item.Ref, orDefault(c.URL, d.obs.Item.URL)
	d.changes = append(d.changes, c)
}

func (d *differ) run() {
	before, now := d.prev.Item, d.obs.Item

	added, removed := labelDiff(before.Labels, now.Labels)
	for _, l := range added {
		by, at := d.claim(func(a Activity) bool { return a.Kind == KindLabelAdded && strings.EqualFold(a.Label, l) })
		d.add(Change{Kind: KindLabelAdded, Label: l, By: by, At: at})
	}
	for _, l := range removed {
		by, at := d.claim(func(a Activity) bool { return a.Kind == KindLabelRemoved && strings.EqualFold(a.Label, l) })
		d.add(Change{Kind: KindLabelRemoved, Label: l, By: by, At: at})
	}

	merged := before.MergedAt == "" && now.MergedAt != ""
	switch {
	case merged:
		by, at := d.claim(func(a Activity) bool { return a.Kind == KindMerged })
		d.claim(func(a Activity) bool { return a.Kind == KindClosed })
		d.add(Change{Kind: KindMerged, By: by, At: at})
	case before.State != "closed" && now.State == "closed":
		by, at := d.claim(func(a Activity) bool { return a.Kind == KindClosed })
		d.add(Change{Kind: KindClosed, By: by, At: at})
	case before.State == "closed" && now.State != "closed":
		by, at := d.claim(func(a Activity) bool { return a.Kind == KindReopened })
		d.add(Change{Kind: KindReopened, By: by, At: at})
	}

	if !sameSet(before.Assignees, now.Assignees) {
		by, at := d.claim(func(a Activity) bool { return a.Kind == KindReassigned })
		d.consumeAll(KindReassigned)
		d.add(Change{Kind: KindReassigned, From: nonNil(before.Assignees), To: nonNil(now.Assignees), By: by, At: at})
	}

	d.edits(before, now)

	var commits []Commit
	for _, a := range d.obs.Activities {
		if !d.fresh(a) {
			continue
		}
		switch a.Kind {
		case KindPushed:
			if a.Commit != nil {
				commits = append(commits, *a.Commit)
			}
		case KindComment, KindInlineComment, KindReview:
			d.add(fromActivity(now, a))
		case KindLabelAdded, KindLabelRemoved, KindClosed, KindReopened, KindMerged:
			// An event the fields do not show: it happened and was undone
			// between two reads. Report it where it is newer than the last read.
			if a.At.After(d.prev.ObservedAt) {
				d.add(fromActivity(now, a))
			}
		}
	}
	if before.Head != "" && now.Head != "" && before.Head != now.Head {
		d.add(Change{Kind: KindPushed, Head: now.Head, Commits: commits, At: d.obs.At})
	}
}

// consumeAll marks every fresh activity of kind as accounted for.
func (d *differ) consumeAll(k Kind) {
	for _, a := range d.obs.Activities {
		if d.fresh(a) && a.Kind == k {
			d.consumed[a.ID] = true
		}
	}
}

// edits holds a title or description edit until its text has held still
// for one poll, then reports it from before the first edit to after the last.
func (d *differ) edits(before, now Item) {
	d.held = map[string]HeldEdit{}
	for f, h := range d.prev.Held {
		d.held[f] = h
	}
	for _, f := range []struct {
		name          string
		before, after string
	}{{"title", before.Title, now.Title}, {"description", before.Body, now.Body}} {
		h, isHeld := d.held[f.name]
		if f.before != f.after {
			by, at := d.claim(func(a Activity) bool { return a.Kind == KindEdited && a.Field == f.name })
			if !isHeld {
				h = HeldEdit{Before: f.before}
			}
			if by != "" || h.By == "" {
				h.By = by
			}
			h.At = at
			d.held[f.name] = h
			continue
		}
		if isHeld {
			d.flushOne(f.name, h, f.after)
		}
	}
	if len(d.held) == 0 {
		d.held = nil
	}
}

func (d *differ) flushHeld() {
	for _, f := range []string{"title", "description"} {
		if h, ok := d.held[f]; ok {
			after := d.obs.Item.Title
			if f == "description" {
				after = d.obs.Item.Body
			}
			d.flushOne(f, h, after)
		}
	}
}

func (d *differ) flushOne(field string, h HeldEdit, after string) {
	delete(d.held, field)
	if h.Before == after {
		return // edited back to where it was
	}
	d.add(Change{Kind: KindEdited, Field: field, Diff: UnifiedDiff(h.Before, after), By: h.By, At: h.At})
}

func fromActivity(it Item, a Activity) Change {
	c := Change{Item: it.Ref, URL: orDefault(a.URL, it.URL), Kind: a.Kind, By: a.By, At: a.At,
		State: a.State, Path: a.Path, Line: a.Line, Hunk: a.Hunk, Label: a.Label}
	if a.Kind == KindComment || a.Kind == KindInlineComment || a.Kind == KindReview {
		c = c.WithBody(a.Body)
	}
	return c
}

func classifyAll(cs []Change, kind ItemKind, r Rules) []Change {
	for i := range cs {
		cs[i].Status, cs[i].Reason = Classify(cs[i], kind, r)
	}
	return cs
}

func mergeSeen(seen []string, acts []Activity) []string {
	out := slices.Clone(seen)
	for _, a := range acts {
		if !slices.Contains(out, a.ID) {
			out = append(out, a.ID)
		}
	}
	slices.Sort(out)
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

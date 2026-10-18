package domain

import "slices"

// Classify decides whether a change wakes the holder or is ignored, and
// why; see the wake contract's "What wakes" and "What never wakes".
func Classify(c Change, kind ItemKind, r Rules) (Status, string) {
	switch c.Kind {
	case KindProblem, KindClosed, KindReopened, KindMerged:
		return StatusPending, ""
	case KindComment, KindInlineComment, KindReview:
		if c.Body != nil && EndsWithMarker(c.whole, r.Marker) {
			return StatusIgnored, "echo: the comment ends with the marker"
		}
		return StatusPending, ""
	case KindLabelAdded, KindLabelRemoved:
		switch {
		case r.Labels.Is(c.Label, MeaningInProgress):
			return StatusIgnored, "echo: the in-progress label is only set by agents"
		case r.Labels.Is(c.Label, MeaningNeedsInput):
			return StatusIgnored, "echo: the needs-input label is only set by agents"
		case kind == ItemPull:
			return StatusIgnored, "labels on pull requests are not watched"
		case r.Labels.Is(c.Label, MeaningSpec), r.Labels.Is(c.Label, MeaningReady):
			return StatusPending, ""
		}
		return StatusIgnored, "the label means nothing to the watch"
	case KindReassigned:
		if kind == ItemPull {
			return StatusIgnored, "assignment on pull requests is not watched"
		}
		if slices.ContainsFunc(c.From, r.IsIdentity) != slices.ContainsFunc(c.To, r.IsIdentity) {
			return StatusPending, ""
		}
		return StatusIgnored, "the assignment neither gave the spec to the identity nor took it away"
	case KindEdited:
		if kind == ItemPull {
			return StatusIgnored, "edits to pull requests are not watched"
		}
		return StatusPending, ""
	case KindPushed:
		if len(c.Commits) == 0 {
			return StatusIgnored, "the head moved with no new commits"
		}
		if slices.ContainsFunc(c.Commits, func(cm Commit) bool { return !r.IsIdentity(cm.Author) }) {
			return StatusPending, ""
		}
		return StatusIgnored, "every new commit is the identity's own"
	}
	return StatusIgnored, "not a kind the wake contract knows"
}

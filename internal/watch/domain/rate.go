package domain

// NearlySpentPercent is the share of a rate limit left below which a
// source is nearly spent: the poll stops reading it until its reset, and
// doctor warns; see sources.md.
const NearlySpentPercent = 5

// NearlySpent says whether a budget of limit requests has fewer than
// NearlySpentPercent of them left. A source that states no limit is never
// nearly spent.
func NearlySpent(remaining, limit int) bool {
	return limit > 0 && remaining*100 < limit*NearlySpentPercent
}

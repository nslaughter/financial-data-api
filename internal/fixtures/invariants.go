package fixtures

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Violation is a failed invariant: the rule's number in the data contract,
// the file and record that break it, and what is wrong.
type Violation struct {
	Rule   int
	File   string
	Record string
	Detail string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: %s: invariant %d: %s", v.File, v.Record, v.Rule, v.Detail)
}

// Violations is every failed invariant, in rule order. It is the error Load
// returns when the fixtures break invariants.
type Violations []Violation

func (vs Violations) Error() string {
	lines := make([]string, len(vs))
	for i, v := range vs {
		lines[i] = v.Error()
	}
	return strings.Join(lines, "\n")
}

var (
	datePattern      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	decimalPattern   = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
)

// parseDate parses a date, reporting whether s is a valid calendar date in
// the form YYYY-MM-DD.
func parseDate(s string) (time.Time, bool) {
	if !datePattern.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

// parseTimestamp parses a timestamp, reporting whether s is a valid UTC
// instant in the form YYYY-MM-DDTHH:MM:SSZ.
func parseTimestamp(s string) (time.Time, bool) {
	if !timestampPattern.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	return t, err == nil
}

// Check returns every invariant of the data contract that f breaks, in rule
// order and then in file order. Rules that compare dates or timestamps as
// instants skip a value that invariant 14 rejects, so they do not repeat its
// report.
func Check(f *Fixtures) Violations {
	c := newChecker(f)
	c.uniqueIdentities()
	c.references()
	c.oneObservationPerPeriod()
	c.sharedObservationFields()
	c.monthlyPeriods()
	c.sequences()
	c.availabilityOrder()
	c.timeOrder()
	c.revisionNumbers()
	c.valuesByChangeType()
	c.withdrawals()
	c.providerCorrections()
	c.receiptOrder()
	c.formats()
	c.oneReleasePerPeriod()
	return c.violations
}

type checker struct {
	f          *Fixtures
	datasets   map[string]bool
	series     map[string]*Series // the first record with each series_id
	obsOrder   []string           // observation_ids in order of first revision
	obsRevs    map[string][]int   // revision indexes by observation_id, in file order
	violations Violations
}

func newChecker(f *Fixtures) *checker {
	c := &checker{
		f:        f,
		datasets: make(map[string]bool),
		series:   make(map[string]*Series),
		obsRevs:  make(map[string][]int),
	}
	for _, d := range f.Datasets {
		c.datasets[d.DatasetID] = true
	}
	for i := range f.Series {
		if _, ok := c.series[f.Series[i].SeriesID]; !ok {
			c.series[f.Series[i].SeriesID] = &f.Series[i]
		}
	}
	for i, r := range f.Revisions {
		if _, ok := c.obsRevs[r.ObservationID]; !ok {
			c.obsOrder = append(c.obsOrder, r.ObservationID)
		}
		c.obsRevs[r.ObservationID] = append(c.obsRevs[r.ObservationID], i)
	}
	return c
}

func (c *checker) add(rule int, file, record, format string, args ...any) {
	c.violations = append(c.violations, Violation{
		Rule:   rule,
		File:   file,
		Record: record,
		Detail: fmt.Sprintf(format, args...),
	})
}

func (c *checker) dataset(i int) string {
	return fmt.Sprintf("datasets[%d] (dataset_id %q)", i, c.f.Datasets[i].DatasetID)
}

func (c *checker) seriesRecord(i int) string {
	return fmt.Sprintf("series[%d] (series_id %q)", i, c.f.Series[i].SeriesID)
}

func (c *checker) revision(i int) string {
	return fmt.Sprintf("revisions[%d] (revision_id %q)", i, c.f.Revisions[i].RevisionID)
}

func (c *checker) release(i int) string {
	r := c.f.Releases[i]
	return fmt.Sprintf("releases[%d] (series_id %q, period_start %q)", i, r.SeriesID, r.PeriodStart)
}

func (c *checker) credential(i int) string {
	return fmt.Sprintf("credentials[%d] (credential_id %q)", i, c.f.Credentials[i].CredentialID)
}

func (c *checker) addRevision(rule, i int, format string, args ...any) {
	c.add(rule, RevisionsFile, c.revision(i), format, args...)
}

// byRevisionNumber returns an observation's revision indexes ordered by
// revision_number, keeping file order among equal numbers.
func (c *checker) byRevisionNumber(revs []int) []int {
	sorted := append([]int(nil), revs...)
	sort.SliceStable(sorted, func(a, b int) bool {
		return c.f.Revisions[sorted[a]].RevisionNumber < c.f.Revisions[sorted[b]].RevisionNumber
	})
	return sorted
}

// datasetRevisions groups revision indexes by the dataset of their series, in
// file order, with datasets in order of first appearance. A revision whose
// series does not exist belongs to no dataset; invariant 2 reports it.
func (c *checker) datasetRevisions() [][]int {
	var (
		groups [][]int
		index  = make(map[string]int)
	)
	for i, r := range c.f.Revisions {
		s, ok := c.series[r.SeriesID]
		if !ok {
			continue
		}
		g, ok := index[s.DatasetID]
		if !ok {
			g = len(groups)
			index[s.DatasetID] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], i)
	}
	return groups
}

// repeats calls report(i, first) for each of n records whose key equals the
// key of an earlier record, first.
func repeats(n int, key func(int) string, report func(i, first int)) {
	seen := make(map[string]int, n)
	for i := range n {
		k := key(i)
		if first, ok := seen[k]; ok {
			report(i, first)
			continue
		}
		seen[k] = i
	}
}

// timed is a revision index with an order key and one of the revision's
// instants.
type timed struct {
	i   int
	key int64
	at  time.Time
}

// outOfOrder calls report(i, j) for each item i whose instant is before the
// latest instant among items with a lower key, j, or, when strict, is not
// after it.
func outOfOrder(items []timed, strict bool, report func(i, j int)) {
	sort.SliceStable(items, func(a, b int) bool { return items[a].key < items[b].key })
	latest := -1    // the item with the latest instant among lower keys
	groupBest := -1 // the item with the latest instant among the current key
	for p, it := range items {
		if p > 0 && it.key != items[p-1].key {
			if latest < 0 || items[groupBest].at.After(items[latest].at) {
				latest = groupBest
			}
			groupBest = -1
		}
		if latest >= 0 {
			prev := items[latest].at
			if it.at.Before(prev) || strict && it.at.Equal(prev) {
				report(it.i, items[latest].i)
			}
		}
		if groupBest < 0 || it.at.After(items[groupBest].at) {
			groupBest = p
		}
	}
}

// Invariant 1: dataset_id, series_id, credential_id, and api_key are each
// unique in their fixture; revision_id is unique across all datasets.
func (c *checker) uniqueIdentities() {
	f := c.f
	repeats(len(f.Datasets), func(i int) string { return f.Datasets[i].DatasetID },
		func(i, first int) {
			c.add(1, DatasetsFile, c.dataset(i), "dataset_id repeats datasets[%d]", first)
		})
	repeats(len(f.Series), func(i int) string { return f.Series[i].SeriesID },
		func(i, first int) {
			c.add(1, SeriesFile, c.seriesRecord(i), "series_id repeats series[%d]", first)
		})
	repeats(len(f.Revisions), func(i int) string { return f.Revisions[i].RevisionID },
		func(i, first int) {
			c.addRevision(1, i, "revision_id repeats revisions[%d]", first)
		})
	repeats(len(f.Credentials), func(i int) string { return f.Credentials[i].CredentialID },
		func(i, first int) {
			c.add(1, CredentialsFile, c.credential(i), "credential_id repeats credentials[%d]", first)
		})
	repeats(len(f.Credentials), func(i int) string { return f.Credentials[i].APIKey },
		func(i, first int) {
			c.add(1, CredentialsFile, c.credential(i), "api_key repeats the api_key of credentials[%d]", first)
		})
}

// Invariant 2: every series names an existing dataset; every revision and
// calendar entry names an existing series; every credential's datasets exist.
func (c *checker) references() {
	for i, s := range c.f.Series {
		if !c.datasets[s.DatasetID] {
			c.add(2, SeriesFile, c.seriesRecord(i), "dataset_id %q names no dataset", s.DatasetID)
		}
	}
	for i, r := range c.f.Revisions {
		if _, ok := c.series[r.SeriesID]; !ok {
			c.addRevision(2, i, "series_id %q names no series", r.SeriesID)
		}
	}
	for i, r := range c.f.Releases {
		if _, ok := c.series[r.SeriesID]; !ok {
			c.add(2, CalendarFile, c.release(i), "series_id %q names no series", r.SeriesID)
		}
	}
	for i, cr := range c.f.Credentials {
		for _, d := range cr.Datasets {
			if !c.datasets[d] {
				c.add(2, CredentialsFile, c.credential(i), "datasets entry %q names no dataset", d)
			}
		}
	}
}

// Invariant 3: observation_id corresponds one to one with (series_id,
// period_start).
func (c *checker) oneObservationPerPeriod() {
	type period struct{ series, start string }
	var (
		periodOf = make(map[string]int) // observation_id to its first revision
		obsOf    = make(map[period]int) // period to the first revision naming it
	)
	for i, r := range c.f.Revisions {
		p := period{r.SeriesID, r.PeriodStart}
		if j, ok := periodOf[r.ObservationID]; ok {
			q := c.f.Revisions[j]
			if (period{q.SeriesID, q.PeriodStart}) != p {
				c.addRevision(3, i, "observation_id %q has series_id %q and period_start %q in revisions[%d]",
					r.ObservationID, q.SeriesID, q.PeriodStart, j)
			}
		} else {
			periodOf[r.ObservationID] = i
		}
		if j, ok := obsOf[p]; ok {
			if q := c.f.Revisions[j]; q.ObservationID != r.ObservationID {
				c.addRevision(3, i, "series_id %q and period_start %q belong to observation_id %q in revisions[%d]",
					r.SeriesID, r.PeriodStart, q.ObservationID, j)
			}
		} else {
			obsOf[p] = i
		}
	}
}

// Invariant 4: all revisions of an observation share series_id,
// period_start, period_end, and unit, and unit equals the series' unit.
func (c *checker) sharedObservationFields() {
	for _, id := range c.obsOrder {
		revs := c.obsRevs[id]
		first := c.f.Revisions[revs[0]]
		for _, i := range revs[1:] {
			r := c.f.Revisions[i]
			var differ []string
			if r.SeriesID != first.SeriesID {
				differ = append(differ, "series_id")
			}
			if r.PeriodStart != first.PeriodStart {
				differ = append(differ, "period_start")
			}
			if r.PeriodEnd != first.PeriodEnd {
				differ = append(differ, "period_end")
			}
			if r.Unit != first.Unit {
				differ = append(differ, "unit")
			}
			if len(differ) > 0 {
				c.addRevision(4, i, "%s differs from revisions[%d] of the same observation",
					strings.Join(differ, ", "), revs[0])
			}
		}
	}
	for i, r := range c.f.Revisions {
		if s, ok := c.series[r.SeriesID]; ok && r.Unit != s.Unit {
			c.addRevision(4, i, "unit %q differs from the series' unit %q", r.Unit, s.Unit)
		}
	}
}

// Invariant 5: for a monthly series, period_start is the first day of a month
// and period_end is the first day of the next month. It applies to revisions
// and calendar entries alike.
func (c *checker) monthlyPeriods() {
	check := func(seriesID, start, end string, report func(format string, args ...any)) {
		s, ok := c.series[seriesID]
		if !ok || s.Frequency != "monthly" {
			return
		}
		startDate, ok := parseDate(start)
		if !ok {
			return
		}
		if startDate.Day() != 1 {
			report("period_start %s is not the first day of a month", start)
		}
		endDate, ok := parseDate(end)
		if !ok {
			return
		}
		next := startDate.AddDate(0, 1, 1-startDate.Day())
		if !endDate.Equal(next) {
			report("period_end %s is not %s, the first day of the month after period_start", end, next.Format("2006-01-02"))
		}
	}
	for i, r := range c.f.Revisions {
		check(r.SeriesID, r.PeriodStart, r.PeriodEnd, func(format string, args ...any) {
			c.addRevision(5, i, format, args...)
		})
	}
	for i, r := range c.f.Releases {
		check(r.SeriesID, r.PeriodStart, r.PeriodEnd, func(format string, args ...any) {
			c.add(5, CalendarFile, c.release(i), format, args...)
		})
	}
}

// Invariant 6: within a dataset, sequence starts at 1 or above, is unique,
// and strictly increases in file order.
func (c *checker) sequences() {
	for _, revs := range c.datasetRevisions() {
		for k, i := range revs {
			r := c.f.Revisions[i]
			if k == 0 {
				if r.Sequence < 1 {
					c.addRevision(6, i, "sequence %d is below 1", r.Sequence)
				}
				continue
			}
			j := revs[k-1]
			if prev := c.f.Revisions[j]; r.Sequence <= prev.Sequence {
				c.addRevision(6, i, "sequence %d is not greater than sequence %d of revisions[%d], which precedes it",
					r.Sequence, prev.Sequence, j)
			}
		}
	}
}

// Invariant 7: within a dataset, available_at never decreases as sequence
// increases.
func (c *checker) availabilityOrder() {
	for _, revs := range c.datasetRevisions() {
		var items []timed
		for _, i := range revs {
			r := c.f.Revisions[i]
			if at, ok := parseTimestamp(r.AvailableAt); ok {
				items = append(items, timed{i: i, key: r.Sequence, at: at})
			}
		}
		outOfOrder(items, false, func(i, j int) {
			q := c.f.Revisions[j]
			c.addRevision(7, i, "available_at %s is before available_at %s of revisions[%d], which has a lower sequence (%d)",
				c.f.Revisions[i].AvailableAt, q.AvailableAt, j, q.Sequence)
		})
	}
}

// Invariant 8: for every revision, published_at ≤ received_at ≤
// available_at.
func (c *checker) timeOrder() {
	for i, r := range c.f.Revisions {
		published, okP := parseTimestamp(r.PublishedAt)
		received, okR := parseTimestamp(r.ReceivedAt)
		available, okA := parseTimestamp(r.AvailableAt)
		if okP && okR && published.After(received) {
			c.addRevision(8, i, "published_at %s is after received_at %s", r.PublishedAt, r.ReceivedAt)
		}
		if okR && okA && received.After(available) {
			c.addRevision(8, i, "received_at %s is after available_at %s", r.ReceivedAt, r.AvailableAt)
		}
		if !okR && okP && okA && published.After(available) {
			c.addRevision(8, i, "published_at %s is after available_at %s", r.PublishedAt, r.AvailableAt)
		}
	}
}

// Invariant 9: an observation's revision_number values are exactly 1 through
// n, and revision 1 is the only initial_release.
func (c *checker) revisionNumbers() {
	for _, id := range c.obsOrder {
		sorted := c.byRevisionNumber(c.obsRevs[id])
		next := int64(1)
		for k, i := range sorted {
			n := c.f.Revisions[i].RevisionNumber
			switch {
			case n == next:
				next++
			case n < 1:
				c.addRevision(9, i, "revision_number %d is below 1", n)
			case n < next:
				c.addRevision(9, i, "revision_number %d repeats the revision_number of revisions[%d]", n, sorted[k-1])
			default:
				c.addRevision(9, i, "revision_number %d skips %d: observation %q has no revision_number %d",
					n, next, id, next)
				next = n + 1
			}
		}
		for _, i := range c.obsRevs[id] {
			r := c.f.Revisions[i]
			if r.RevisionNumber == 1 && r.ChangeType != InitialRelease {
				c.addRevision(9, i, "revision_number 1 has change_type %q, not %s", r.ChangeType, InitialRelease)
			}
			if r.RevisionNumber != 1 && r.ChangeType == InitialRelease {
				c.addRevision(9, i, "%s has revision_number %d, not 1", InitialRelease, r.RevisionNumber)
			}
		}
	}
}

// Invariant 10: value, missing_reason, and change_type combine only as the
// change types table allows, and a decimal value matches the decimal pattern.
func (c *checker) valuesByChangeType() {
	for i, r := range c.f.Revisions {
		if r.Value != nil && !decimalPattern.MatchString(*r.Value) {
			c.addRevision(10, i, "value %q does not match %s", *r.Value, decimalPattern)
		}
		switch r.ChangeType {
		case InitialRelease, SourceRevision:
			if r.Value == nil && r.MissingReason == nil {
				c.addRevision(10, i, "%s with a null value has no missing_reason", r.ChangeType)
			}
			if r.Value != nil && r.MissingReason != nil {
				c.addRevision(10, i, "%s with a value has missing_reason %q", r.ChangeType, *r.MissingReason)
			}
		case ProviderCorrection:
			if r.Value == nil {
				c.addRevision(10, i, "%s has a null value", r.ChangeType)
			}
			if r.MissingReason != nil {
				c.addRevision(10, i, "%s has missing_reason %q", r.ChangeType, *r.MissingReason)
			}
		case Withdrawal:
			if r.Value != nil {
				c.addRevision(10, i, "%s has value %q", r.ChangeType, *r.Value)
			}
			if r.MissingReason != nil {
				c.addRevision(10, i, "%s has missing_reason %q", r.ChangeType, *r.MissingReason)
			}
		default:
			c.addRevision(10, i, "change_type %q is not a change type", r.ChangeType)
		}
	}
}

// Invariant 11: a withdrawal does not follow another withdrawal, by
// revision_number.
func (c *checker) withdrawals() {
	for _, id := range c.obsOrder {
		sorted := c.byRevisionNumber(c.obsRevs[id])
		for k := 1; k < len(sorted); k++ {
			i, j := sorted[k], sorted[k-1]
			if c.f.Revisions[i].ChangeType == Withdrawal && c.f.Revisions[j].ChangeType == Withdrawal {
				c.addRevision(11, i, "%s follows the %s revisions[%d]", Withdrawal, Withdrawal, j)
			}
		}
	}
}

// Invariant 12: a provider_correction with number n corrects revision n − 1,
// which is not a withdrawal, and has the same published_at and received_at as
// that revision.
func (c *checker) providerCorrections() {
	for _, id := range c.obsOrder {
		revs := c.obsRevs[id]
		byNumber := make(map[int64]int, len(revs))
		for _, i := range revs {
			if _, ok := byNumber[c.f.Revisions[i].RevisionNumber]; !ok {
				byNumber[c.f.Revisions[i].RevisionNumber] = i
			}
		}
		for _, i := range revs {
			r := c.f.Revisions[i]
			if r.ChangeType != ProviderCorrection {
				continue
			}
			j, ok := byNumber[r.RevisionNumber-1]
			if !ok {
				c.addRevision(12, i, "observation %q has no revision_number %d for this %s to correct",
					id, r.RevisionNumber-1, ProviderCorrection)
				continue
			}
			q := c.f.Revisions[j]
			if q.ChangeType == Withdrawal {
				c.addRevision(12, i, "corrects the %s revisions[%d]", Withdrawal, j)
			}
			same := func(name, a, b string) {
				ta, okA := parseTimestamp(a)
				tb, okB := parseTimestamp(b)
				if okA && okB && !ta.Equal(tb) {
					c.addRevision(12, i, "%s %s differs from %s %s of the corrected revisions[%d]", name, a, name, b, j)
				}
			}
			same("published_at", r.PublishedAt, q.PublishedAt)
			same("received_at", r.ReceivedAt, q.ReceivedAt)
		}
	}
}

// Invariant 13: among an observation's revisions other than provider
// corrections, received_at strictly increases with revision_number.
func (c *checker) receiptOrder() {
	for _, id := range c.obsOrder {
		var items []timed
		for _, i := range c.obsRevs[id] {
			r := c.f.Revisions[i]
			if r.ChangeType == ProviderCorrection {
				continue
			}
			if at, ok := parseTimestamp(r.ReceivedAt); ok {
				items = append(items, timed{i: i, key: r.RevisionNumber, at: at})
			}
		}
		outOfOrder(items, true, func(i, j int) {
			q := c.f.Revisions[j]
			c.addRevision(13, i, "received_at %s is not after received_at %s of revisions[%d], which has a lower revision_number (%d)",
				c.f.Revisions[i].ReceivedAt, q.ReceivedAt, j, q.RevisionNumber)
		})
	}
}

// Invariant 14: dates are valid YYYY-MM-DD calendar dates, and timestamps
// match YYYY-MM-DDTHH:MM:SSZ and are valid UTC instants.
func (c *checker) formats() {
	check := func(dates, timestamps [][2]string, report func(format string, args ...any)) {
		for _, d := range dates {
			if _, ok := parseDate(d[1]); !ok {
				report("%s %q is not a valid YYYY-MM-DD date", d[0], d[1])
			}
		}
		for _, ts := range timestamps {
			if _, ok := parseTimestamp(ts[1]); !ok {
				report("%s %q is not a valid YYYY-MM-DDTHH:MM:SSZ timestamp", ts[0], ts[1])
			}
		}
	}
	for i, r := range c.f.Revisions {
		check(
			[][2]string{{"period_start", r.PeriodStart}, {"period_end", r.PeriodEnd}},
			[][2]string{{"published_at", r.PublishedAt}, {"received_at", r.ReceivedAt}, {"available_at", r.AvailableAt}},
			func(format string, args ...any) { c.addRevision(14, i, format, args...) },
		)
	}
	for i, r := range c.f.Releases {
		check(
			[][2]string{{"period_start", r.PeriodStart}, {"period_end", r.PeriodEnd}},
			[][2]string{{"scheduled_at", r.ScheduledAt}},
			func(format string, args ...any) { c.add(14, CalendarFile, c.release(i), format, args...) },
		)
	}
}

// Invariant 15: the release calendar has at most one entry per series and
// period. A monthly period is identified by its period_start, as in
// invariant 3.
func (c *checker) oneReleasePerPeriod() {
	f := c.f
	repeats(len(f.Releases), func(i int) string { return f.Releases[i].SeriesID + "\x00" + f.Releases[i].PeriodStart },
		func(i, first int) {
			c.add(15, CalendarFile, c.release(i), "repeats the series and period of releases[%d]", first)
		})
}

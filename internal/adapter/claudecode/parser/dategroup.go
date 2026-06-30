package parser

import "time"

// DateCategory labels a group of sessions by relative recency.
type DateCategory string

const (
	DateToday     DateCategory = "Today"
	DateYesterday DateCategory = "Yesterday"
	DateThisWeek  DateCategory = "This Week"
	DateThisMonth DateCategory = "This Month"
	DateOlder     DateCategory = "Older"
)

// DateGroup holds a category label and its matching sessions.
type DateGroup struct {
	Category DateCategory
	Sessions []SessionInfo
}

// GroupSessionsByDate buckets sessions into date categories by ModTime,
// returning non-empty groups in display order. Input order is preserved
// within each group (caller pre-sorts newest-first).
func GroupSessionsByDate(sessions []SessionInfo) []DateGroup {
	return groupSessionsByDateAt(sessions, time.Now())
}

// groupSessionsByDateAt is the testable core with an explicit "now".
func groupSessionsByDateAt(sessions []SessionInfo, now time.Time) []DateGroup {
	// Boundaries in the local timezone.
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	weekStart := todayStart.AddDate(0, 0, -7)
	monthStart := todayStart.AddDate(0, 0, -30)

	categories := []DateCategory{DateToday, DateYesterday, DateThisWeek, DateThisMonth, DateOlder}
	buckets := make(map[DateCategory][]SessionInfo, len(categories))

	for _, s := range sessions {
		t := s.ModTime
		var cat DateCategory
		switch {
		case !t.Before(todayStart):
			cat = DateToday
		case !t.Before(yesterdayStart):
			cat = DateYesterday
		case !t.Before(weekStart):
			cat = DateThisWeek
		case !t.Before(monthStart):
			cat = DateThisMonth
		default:
			cat = DateOlder
		}
		buckets[cat] = append(buckets[cat], s)
	}

	var groups []DateGroup
	for _, cat := range categories {
		if ss := buckets[cat]; len(ss) > 0 {
			groups = append(groups, DateGroup{Category: cat, Sessions: ss})
		}
	}
	return groups
}

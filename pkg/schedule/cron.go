package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronField represents matched values for a single cron field.
type CronField struct {
	raw    string
	values map[int]bool
	min    int
	max    int
}

// CronExpression represents a parsed 5-part cron schedule.
type CronExpression struct {
	expr    string
	minute  CronField
	hour    CronField
	dom     CronField
	month   CronField
	dow     CronField
	loc     *time.Location
}

// ParseCron parses a 5-field standard cron string: "minute hour day-of-month month day-of-week".
func ParseCron(expr string, loc ...*time.Location) (*CronExpression, error) {
	expr = strings.TrimSpace(expr)
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("schedule: invalid cron expression '%s': expected 5 fields, got %d", expr, len(fields))
	}

	location := time.Local
	if len(loc) > 0 && loc[0] != nil {
		location = loc[0]
	}

	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("schedule: invalid minute field '%s': %w", fields[0], err)
	}

	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("schedule: invalid hour field '%s': %w", fields[1], err)
	}

	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("schedule: invalid day-of-month field '%s': %w", fields[2], err)
	}

	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("schedule: invalid month field '%s': %w", fields[3], err)
	}

	// 0=Sunday, 6=Saturday (7 is also mapped to 0)
	dow, err := parseField(fields[4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("schedule: invalid day-of-week field '%s': %w", fields[4], err)
	}
	if dow.values[7] {
		dow.values[0] = true
		delete(dow.values, 7)
	}

	return &CronExpression{
		expr:   expr,
		minute: minute,
		hour:   hour,
		dom:    dom,
		month:  month,
		dow:    dow,
		loc:    location,
	}, nil
}

// String returns the raw cron expression.
func (c *CronExpression) String() string {
	return c.expr
}

// Matches returns true if the cron expression matches the given time down to the minute.
func (c *CronExpression) Matches(t time.Time) bool {
	if c.loc != nil {
		t = t.In(c.loc)
	}

	if !c.minute.values[t.Minute()] {
		return false
	}
	if !c.hour.values[t.Hour()] {
		return false
	}
	if !c.month.values[int(t.Month())] {
		return false
	}

	// Standard cron: if both DOM and DOW are specified (not wildcard '*'),
	// then it matches if EITHER matches. If either is '*', it matches if both match.
	domStar := c.dom.raw == "*"
	dowStar := c.dow.raw == "*"

	if !domStar && !dowStar {
		return c.dom.values[t.Day()] || c.dow.values[int(t.Weekday())]
	}

	if !c.dom.values[t.Day()] {
		return false
	}
	if !c.dow.values[int(t.Weekday())] {
		return false
	}

	return true
}

// Next calculates the next occurrence after the given time (up to 5 years into the future).
func (c *CronExpression) Next(t time.Time) time.Time {
	if c.loc != nil {
		t = t.In(c.loc)
	}

	// Truncate to current minute and step 1 minute forward
	current := t.Truncate(time.Minute).Add(time.Minute)
	limit := current.AddDate(5, 0, 0)

	for current.Before(limit) {
		if c.Matches(current) {
			return current
		}
		current = current.Add(time.Minute)
	}

	return time.Time{}
}

func parseField(field string, min, max int) (CronField, error) {
	res := CronField{
		raw:    field,
		values: make(map[int]bool),
		min:    min,
		max:    max,
	}

	if field == "*" {
		for i := min; i <= max; i++ {
			res.values[i] = true
		}
		return res, nil
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		step := 1

		if strings.Contains(part, "/") {
			subParts := strings.Split(part, "/")
			if len(subParts) != 2 {
				return res, fmt.Errorf("invalid step syntax '%s'", part)
			}
			s, err := strconv.Atoi(subParts[1])
			if err != nil || s <= 0 {
				return res, fmt.Errorf("invalid step value '%s'", subParts[1])
			}
			step = s
			part = subParts[0]
		}

		var rangeStart, rangeEnd int
		if part == "*" {
			rangeStart = min
			rangeEnd = max
		} else if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return res, fmt.Errorf("invalid range syntax '%s'", part)
			}
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 != nil || err2 != nil || start > end || start < min || end > max {
				return res, fmt.Errorf("invalid range limits in '%s'", part)
			}
			rangeStart = start
			rangeEnd = end
		} else {
			num, err := strconv.Atoi(part)
			if err != nil || num < min || num > max {
				return res, fmt.Errorf("number '%s' outside valid range [%d, %d]", part, min, max)
			}
			rangeStart = num
			rangeEnd = num
		}

		for val := rangeStart; val <= rangeEnd; val += step {
			res.values[val] = true
		}
	}

	if len(res.values) == 0 {
		return res, fmt.Errorf("no valid values parsed for field '%s'", field)
	}

	return res, nil
}

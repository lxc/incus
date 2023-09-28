package instance

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GetExpiry returns the expiry date based on the reference date and a length of time.
// The length of time format is "<integer>(S|M|H|d|w|m|y)", and can contain multiple such fields, e.g.
// "1d 3H" (1 day and 3 hours).
func GetExpiry(refDate time.Time, s string) (time.Time, error) {
	expr := strings.TrimSpace(s)

	if expr == "" {
		return time.Time{}, nil
	}

	re, err := regexp.Compile(`^(\d+)(S|M|H|d|w|m|y)$`)
	if err != nil {
		return time.Time{}, err
	}

	expiry := map[string]int{
		"S": 0,
		"M": 0,
		"H": 0,
		"d": 0,
		"w": 0,
		"m": 0,
		"y": 0,
	}

	values := strings.Split(expr, " ")

	if len(values) == 0 {
		return time.Time{}, nil
	}

	for _, value := range values {
		fields := re.FindStringSubmatch(value)
		if fields == nil {
			return time.Time{}, fmt.Errorf("Invalid expiry expression")
		}

		if expiry[fields[2]] > 0 {
			// We don't allow fields to be set multiple times
			return time.Time{}, fmt.Errorf("Invalid expiry expression")
		}

		val, err := strconv.Atoi(fields[1])
		if err != nil {
			return time.Time{}, err
		}

		expiry[fields[2]] = val
	}

	t := refDate.AddDate(expiry["y"], expiry["m"], expiry["d"]+expiry["w"]*7).Add(
		time.Hour*time.Duration(expiry["H"]) + time.Minute*time.Duration(expiry["M"]) + time.Second*time.Duration(expiry["S"]))

	return t, nil
}

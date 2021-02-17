package timeutils

import (
	"errors"
	"regexp"
	"strconv"
)

var (
	// ErrFrequencyCannotBeZeroOrLess frequency cannot be below zero
	ErrFrequencyCannotBeZeroOrLess = errors.New(`"evaluate every" cannot be zero or below`)

	// ErrFrequencyCouldNotBeParsed frequency cannot be parsed
	ErrFrequencyCouldNotBeParsed = errors.New(`"evaluate every" field could not be parsed`)
)

var (
	valueFormatRegex = regexp.MustCompile(`^\d+`)
	unitFormatRegex  = regexp.MustCompile(`\w{1}$`)
)

var unitMultiplier = map[string]int{
	"s": 1,
	"m": 60,
	"h": 3600,
	"d": 86400,
}

func GetTimeDurationStringToSeconds(str string) (int64, error) {
	multiplier := 1

	matches := valueFormatRegex.FindAllString(str, 1)

	if len(matches) == 0 {
		return 0, ErrFrequencyCouldNotBeParsed
	}

	value, err := strconv.Atoi(matches[0])
	if err != nil {
		return 0, err
	}

	if value == 0 {
		return 0, ErrFrequencyCannotBeZeroOrLess
	}

	unit := unitFormatRegex.FindAllString(str, 1)[0]

	if val, ok := unitMultiplier[unit]; ok {
		multiplier = val
	}

	return int64(value * multiplier), nil
}

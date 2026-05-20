package version

import (
	"strconv"
	"strings"
)

const AppName = "Go Magnifier"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = ""
)

func Display() string {
	if strings.TrimSpace(Version) == "" {
		return "dev"
	}
	return Version
}

func AppTitle() string {
	return AppName + " " + Display()
}

func OverlayTitle() string {
	return AppName + " Overlay " + Display()
}

func Tooltip() string {
	return AppName + " " + Display()
}

func Details() string {
	parts := []string{AppName + " " + Display()}
	if strings.TrimSpace(Commit) != "" && Commit != "unknown" {
		parts = append(parts, "커밋: "+Commit)
	}
	if strings.TrimSpace(BuildTime) != "" {
		parts = append(parts, "빌드 시각(UTC): "+BuildTime)
	}
	return strings.Join(parts, "\r\n")
}

func Base() string {
	value := strings.TrimSpace(Display())
	value = strings.TrimPrefix(strings.TrimPrefix(value, "v"), "V")
	if cut := strings.IndexAny(value, "+-"); cut >= 0 {
		value = value[:cut]
	}
	return value
}

func ParseComparable(value string) ([3]int, bool) {
	var parsed [3]int
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "v"), "V")
	if cut := strings.IndexAny(trimmed, "+-"); cut >= 0 {
		trimmed = trimmed[:cut]
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 3 {
		return parsed, false
	}
	for index := 0; index < 3; index++ {
		number, err := strconv.Atoi(parts[index])
		if err != nil {
			return parsed, false
		}
		parsed[index] = number
	}
	return parsed, true
}
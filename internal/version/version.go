package version

import "strings"

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
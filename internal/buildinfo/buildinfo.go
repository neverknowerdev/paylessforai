// Package buildinfo contains values injected into official release binaries.
package buildinfo

import "runtime"

var (
	Version            = "dev"
	Commit             = "unknown"
	Channel            = "dev"
	BuiltAt            = "unknown"
	Official           = "false"
	SupervisorProtocol = "1"
	UpdatePublicKey    = ""
)

type Info struct {
	Version            string `json:"version"`
	Commit             string `json:"commit"`
	Channel            string `json:"channel"`
	BuiltAt            string `json:"built_at"`
	Official           bool   `json:"official"`
	OS                 string `json:"os"`
	Arch               string `json:"arch"`
	SupervisorProtocol string `json:"supervisor_protocol"`
}

func Current() Info {
	return Info{Version: Version, Commit: Commit, Channel: Channel, BuiltAt: BuiltAt,
		Official: Official == "true", OS: runtime.GOOS, Arch: runtime.GOARCH,
		SupervisorProtocol: SupervisorProtocol}
}

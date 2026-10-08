package live

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

type Endpoint struct {
	ApplianceID      string `json:"appliance_id"`
	Role             string `json:"role"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	KeyRef           string `json:"key_ref"`
	ReceiverID       string `json:"receiver_id,omitempty"`
	CollectionMode   string `json:"collection_mode,omitempty"`
	NativeRoute      string `json:"native_route,omitempty"`
	NativeSourceUUID string `json:"native_source_uuid,omitempty"`
}

type Settings struct {
	BandwidthKiB int `json:"bandwidth_kib"`
	TimeoutMins  int `json:"timeout_minutes"`
}

type Request struct {
	Action           string `json:"action"`
	SourceID         string `json:"source_id"`
	TargetID         string `json:"target_id,omitempty"`
	CollectionID     string `json:"collection_id,omitempty"`
	Timestamp        string `json:"timestamp,omitempty"`
	Confirmation     string `json:"confirmation"`
	Quiesced         bool   `json:"quiesced"`
	CompatibleTarget bool   `json:"compatible_target"`
}

type Job struct {
	ID             string          `json:"id"`
	Request        Request         `json:"request"`
	State          string          `json:"state"`
	Phase          string          `json:"phase"`
	Message        string          `json:"message"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	NativeSnapshot *NativeSnapshot `json:"native_snapshot,omitempty"`
}

type Preflight struct {
	EndpointID string `json:"endpoint_id"`
	Role       string `json:"role"`
	Evidence   string `json:"evidence"`
	Warning    string `json:"warning"`
}

var (
	refPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	idPattern   = regexp.MustCompile(`^(app|job)_[a-f0-9]{32}$`)
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
)

func ValidateEndpoint(e Endpoint) error {
	if !idPattern.MatchString(e.ApplianceID) || !strings.HasPrefix(e.ApplianceID, "app_") {
		return fmt.Errorf("invalid appliance ID")
	}
	if e.Role != "source" && e.Role != "receiver" && e.Role != "restore" {
		return fmt.Errorf("role must be source, receiver, or restore")
	}
	if net.ParseIP(e.Host) == nil && !hostPattern.MatchString(e.Host) {
		return fmt.Errorf("invalid SSH hostname or IP address")
	}
	if e.Port < 1 || e.Port > 65535 {
		return fmt.Errorf("SSH port must be between 1 and 65535")
	}
	if !refPattern.MatchString(e.KeyRef) || e.KeyRef == "known_hosts" {
		return fmt.Errorf("key reference must be a simple secret filename without an extension")
	}
	if e.Role == "source" {
		if e.CollectionMode == "native_push" {
			if e.ReceiverID != "" || e.Port != 122 {
				return fmt.Errorf("native push requires port 122 and no dedicated receiver mapping")
			}
			return nil
		}
		if e.CollectionMode != "" {
			return fmt.Errorf("unknown collection mode")
		}
		if e.NativeRoute != "" || e.NativeSourceUUID != "" {
			return fmt.Errorf("native binding requires native push mode")
		}
		if !idPattern.MatchString(e.ReceiverID) || !strings.HasPrefix(e.ReceiverID, "app_") || e.ReceiverID == e.ApplianceID {
			return fmt.Errorf("source requires a different registered receiver")
		}
	} else if e.ReceiverID != "" || e.CollectionMode != "" || e.NativeRoute != "" || e.NativeSourceUUID != "" {
		return fmt.Errorf("only a source can specify a receiver")
	}
	return nil
}

func ValidateSettings(s Settings) error {
	if s.BandwidthKiB < 0 || s.BandwidthKiB > 1048576 {
		return fmt.Errorf("bandwidth limit must be between 0 and 1048576 KiB/s; zero means unlimited")
	}
	if s.TimeoutMins < 1 || s.TimeoutMins > 1440 {
		return fmt.Errorf("operation timeout must be between 1 and 1440 minutes")
	}
	return nil
}

func validTimestamp(value string) bool {
	parsed, err := time.Parse("20060102T150405", value)
	return err == nil && parsed.Format("20060102T150405") == value
}

package hub

import "time"

// AgentInfo describes a connected agent and is used for status reporting and
// observability.
type AgentInfo struct {
	ID           string    `json:"id"`
	ConnectedAt  time.Time `json:"connectedAt"`
	LastSeen     time.Time `json:"lastSeen"`
	MySQLVersion string    `json:"mySQLVersion,omitempty"`
	Status       string    `json:"status"` // "online" or "offline"
}

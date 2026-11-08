//go:build !linux

package gateway

// collectHost has no data source off Linux (/proc, statfs); the node reports that honestly.
func collectHost(n *nodeInfo) {
	const msg = "host metrics are only available on Linux"
	n.CPU["error"], n.Memory["error"], n.Disk["error"] = msg, msg, msg
}

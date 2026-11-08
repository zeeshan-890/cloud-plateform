package gateway

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Everything below turns raw connectivity facts into messages a person can act on.

type checkResult struct {
	Peer  string  `json:"peer"`
	Path  string  `json:"path"` // private | public
	Addr  string  `json:"addr"`
	Port  int     `json:"port"`
	OK    bool    `json:"ok"`
	Ms    float64 `json:"ms,omitempty"`
	Error string  `json:"error,omitempty"`
}

type diagnostic struct {
	Level   string `json:"level"` // error | warn | ok | info
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

type joinReport struct {
	At          time.Time     `json:"at"`
	Hostname    string        `json:"hostname"`
	PrivateIP   string        `json:"private_ip"`
	PrivateCIDR string        `json:"private_cidr"`
	PublicIP    string        `json:"public_ip"`
	Provider    string        `json:"provider"`
	OS          string        `json:"os"`
	Firewall    string        `json:"firewall"`
	Docker      bool          `json:"docker"`
	Opened      []int         `json:"opened_ports"`
	Listening   []int         `json:"listening_ports"`
	Checks      []checkResult `json:"checks"`  // this server -> peers
	Inbound     []checkResult `json:"inbound"` // control plane -> this server
	Diagnostics []diagnostic  `json:"diagnostics"`
	Status      string        `json:"status"` // ok | warn | error
}

func providerLabel(p string) string {
	switch p {
	case "oracle":
		return "Oracle Cloud"
	case "aws":
		return "AWS"
	case "gcp":
		return "Google Cloud"
	case "azure":
		return "Azure"
	}
	return "your cloud provider"
}

// cloudFix tells the person where to open a port, naming the provider's own firewall.
func cloudFix(provider string, peer string, ports []int, from string) string {
	pl := portList(ports)
	// from is used inside a firewall command, so it must be an address or network; otherwise show a placeholder.
	cmdFrom := from
	if _, _, err := net.ParseCIDR(from); err != nil && net.ParseIP(from) == nil {
		cmdFrom = "<source-ip>"
	}
	switch provider {
	case "oracle":
		return fmt.Sprintf("On %s open TCP %s from %s: (1) in the Oracle Console add an ingress rule to the subnet's Security List or a Network Security Group attached to the server's VNIC, and (2) allow it in the server's own firewall, for example: sudo firewall-cmd --permanent --add-rich-rule='rule family=ipv4 source address=%s port port=<port> protocol=tcp accept' && sudo firewall-cmd --reload", peer, pl, from, cmdFrom)
	case "aws":
		return fmt.Sprintf("On %s open TCP %s from %s in the instance's Security Group and in the server's own firewall.", peer, pl, from)
	case "gcp":
		return fmt.Sprintf("On %s open TCP %s from %s with a VPC firewall rule and in the server's own firewall.", peer, pl, from)
	case "azure":
		return fmt.Sprintf("On %s open TCP %s from %s in the Network Security Group and in the server's own firewall.", peer, pl, from)
	}
	return fmt.Sprintf("On %s open TCP %s from %s in your cloud firewall / security group and in the server's own firewall.", peer, pl, from)
}

func portList(ports []int) string {
	s := make([]string, 0, len(ports))
	for _, p := range ports {
		s = append(s, fmt.Sprint(p))
	}
	return strings.Join(s, ", ")
}

func ipIn(cidr, ip string) bool {
	_, n, err := net.ParseCIDR(cidr)
	p := net.ParseIP(ip)
	return err == nil && p != nil && n.Contains(p)
}

func sameSlash16(a, b string) bool {
	x, y := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	return x != nil && y != nil && x[0] == y[0] && x[1] == y[1]
}

// diagnose builds the human-readable findings for one server.
func diagnose(self serverEntry, rep *joinReport, peers []serverEntry) []diagnostic {
	var out []diagnostic
	add := func(level, code, msg, fix string) { out = append(out, diagnostic{level, code, msg, fix}) }
	byName := map[string]serverEntry{}
	for _, p := range peers {
		byName[p.Name] = p
	}
	needsPrivate := self.Prefer == "private"
	for _, p := range peers {
		if p.PrivateAddr != "" {
			needsPrivate = true
		}
	}

	// ---- this server's own network facts
	if rep.PrivateIP == "" {
		if needsPrivate {
			add("error", "no_private_address", "This server has no private network address, but the cluster uses private links.",
				"Attach the server to the same private network (VCN/VPC subnet) as the other servers, or set Traffic to \"Public internet\".")
		}
	} else if self.PrivateAddr != "" && self.PrivateAddr != rep.PrivateIP {
		add("warn", "private_address_differs", fmt.Sprintf("The saved private address %s is not the one this server reports (%s).", self.PrivateAddr, rep.PrivateIP),
			"Edit the server and set the private address to "+rep.PrivateIP+".")
	}
	if rep.PrivateIP != "" && rep.PrivateCIDR != "" {
		for _, p := range peers {
			if p.PrivateAddr == "" || net.ParseIP(p.PrivateAddr) == nil || ipIn(rep.PrivateCIDR, p.PrivateAddr) {
				continue
			}
			if sameSlash16(rep.PrivateIP, p.PrivateAddr) {
				add("warn", "different_subnet", fmt.Sprintf("%s (%s) is in a different subnet from this server (%s). Traffic works only if the subnets are routed to each other.", p.Name, p.PrivateAddr, rep.PrivateCIDR),
					"Same VCN/VPC with different subnets is fine when route tables and security rules allow it; otherwise put both servers in one subnet.")
			} else {
				add("error", "private_network_mismatch", fmt.Sprintf("%s has private address %s, which is not in this server's private network %s. These servers are not on the same private network, so a private link cannot work.", p.Name, p.PrivateAddr, rep.PrivateCIDR),
					"Move both servers into the same VCN/VPC (or peer the networks), or set Traffic between servers to \"Public internet\" for this server.")
			}
		}
	}
	if rep.Firewall != "" && rep.Firewall != "none" {
		opened := map[int]bool{}
		for _, p := range rep.Opened {
			opened[p] = true
		}
		var missing []int
		for _, sp := range self.Ports {
			if !opened[sp.Port] {
				missing = append(missing, sp.Port)
			}
		}
		if len(missing) > 0 {
			add("warn", "host_firewall_not_opened", fmt.Sprintf("The join script could not open port(s) %s in this server's own firewall (%s).", portList(missing), rep.Firewall),
				"Re-run the command as root (sudo), or open the port(s) yourself in "+rep.Firewall+".")
		}
	}
	var notListening []int
	listen := map[int]bool{}
	for _, p := range rep.Listening {
		listen[p] = true
	}
	for _, sp := range self.Ports {
		if !listen[sp.Port] {
			notListening = append(notListening, sp.Port)
		}
	}
	if len(notListening) > 0 && len(self.Ports) > 0 {
		add("info", "not_listening", fmt.Sprintf("Nothing is listening on port(s) %s on this server yet.", portList(notListening)), "That is expected before the service is started.")
	}

	// ---- this server -> peers
	type key struct{ peer, path string }
	group := map[key][]checkResult{}
	for _, c := range rep.Checks {
		k := key{c.Peer, c.Path}
		group[k] = append(group[k], c)
	}
	keys := make([]key, 0, len(group))
	for k := range group {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].peer != keys[j].peer {
			return keys[i].peer < keys[j].peer
		}
		return keys[i].path < keys[j].path
	})
	from := rep.PrivateIP
	if rep.PrivateCIDR != "" {
		from = rep.PrivateCIDR
	}
	failedPrivateOnly := map[string]bool{}
	for _, k := range keys {
		cs := group[k]
		var okp, bad, closed []int
		for _, c := range cs {
			switch {
			case c.OK:
				okp = append(okp, c.Port)
			case c.Error == "refused":
				closed = append(closed, c.Port) // the path works; nothing is listening
			default:
				bad = append(bad, c.Port)
			}
		}
		where := "private network"
		src := from
		if k.path == "public" {
			where, src = "public internet", rep.PublicIP
			if src == "" {
				src = "this server's public IP"
			}
		}
		addr := cs[0].Addr
		if len(closed) > 0 {
			add("warn", "peer_port_closed", fmt.Sprintf("%s (%s) answered on the %s, but nothing is listening on port(s) %s (connection refused). The network and firewalls allow the connection.", k.peer, addr, where, portList(closed)),
				"Start the service on "+k.peer+" that should listen on that port, then press Re-check.")
			okp = append(okp, closed...)
		}
		switch {
		case len(bad) == 0 && len(okp) == len(closed):
			// only closed ports: already reported above
		case len(bad) == 0:
			var ms float64
			var nOK int
			var okOnly []int
			for _, c := range cs {
				if c.OK {
					ms += c.Ms
					nOK++
					okOnly = append(okOnly, c.Port)
				}
			}
			add("ok", "reachable", fmt.Sprintf("Reached %s at %s over the %s on port(s) %s (about %.0f ms).", k.peer, addr, where, portList(okOnly), ms/float64(nOK)), "")
		case len(okp) == 0:
			code := "peer_unreachable_" + k.path
			add("error", code, fmt.Sprintf("Cannot reach %s at %s over the %s on any tested port (%s).", k.peer, addr, where, portList(bad)),
				"Nothing got through, so a firewall is probably dropping the traffic. "+cloudFix(rep.Provider, k.peer, bad, src))
			if k.path == "private" {
				failedPrivateOnly[k.peer] = true
			}
		default:
			add("error", "port_blocked", fmt.Sprintf("Port(s) %s on %s (%s) are blocked over the %s, although port(s) %s get through.", portList(bad), k.peer, addr, where, portList(okp)),
				"The network path works, so only those ports are filtered. "+cloudFix(rep.Provider, k.peer, bad, src))
			if k.path == "private" {
				failedPrivateOnly[k.peer] = true
			}
		}
	}
	// private broken but public fine for a server that should use the private path
	for _, k := range keys {
		if k.path != "public" {
			continue
		}
		for _, c := range group[k] {
			if c.OK && failedPrivateOnly[k.peer] && self.Prefer == "private" {
				add("warn", "would_fail_over_to_public", fmt.Sprintf("The private link to %s is broken but the public one works, so traffic would leave over the public internet.", k.peer),
					"Fix the private path above, or set Traffic to \"Public internet\" on purpose.")
				break
			}
		}
	}
	for _, p := range peers {
		if len(p.Ports) == 0 {
			add("info", "peer_no_ports", fmt.Sprintf("%s has no ports configured, so nothing was tested against it.", p.Name), "Add its ports in the Servers panel to test them.")
		}
	}
	if len(peers) == 0 {
		add("info", "no_peers", "This is the only server recorded, so there was nothing to connect to yet.", "Add another server and run its join command to test the links.")
	}

	// ---- control plane -> this server
	for _, path := range []string{"private", "public"} {
		var blocked, closed, good []int
		var addr string
		for _, c := range rep.Inbound {
			if c.Path != path {
				continue
			}
			addr = c.Addr
			switch {
			case c.OK:
				good = append(good, c.Port)
			case c.Error == "refused":
				closed = append(closed, c.Port)
			default:
				blocked = append(blocked, c.Port)
			}
		}
		net := path + " network"
		if path == "public" {
			net = "public internet"
		}
		if len(blocked) > 0 {
			add("warn", "inbound_"+path+"_blocked", fmt.Sprintf("The control plane could not reach port(s) %s on this server at %s over the %s (no answer).", portList(blocked), addr, net),
				"This only matters if the control plane itself has to talk to those ports. "+cloudFix(rep.Provider, self.Name, blocked, "the control plane's address"))
		}
		if len(closed) > 0 {
			add("info", "inbound_"+path+"_closed", fmt.Sprintf("Port(s) %s on this server are open to the control plane over the %s, but nothing is listening yet (connection refused).", portList(closed), net),
				"")
		}
		if len(good) > 0 {
			add("ok", "inbound_"+path+"_ok", fmt.Sprintf("The control plane reached port(s) %s on this server over the %s.", portList(good), net), "")
		}
	}
	return out
}

func summarize(d []diagnostic) string {
	st := "ok"
	for _, x := range d {
		if x.Level == "error" {
			return "error"
		}
		if x.Level == "warn" {
			st = "warn"
		}
	}
	return st
}

// textReport is what the join script prints at the end.
func textReport(name string, rep *joinReport) string {
	var b strings.Builder
	icon := map[string]string{"ok": "[ OK ]", "warn": "[WARN]", "error": "[FAIL]", "info": "[info]"}
	fmt.Fprintf(&b, "\nServer %q reported to the control plane.\n\n", name)
	for _, d := range rep.Diagnostics {
		fmt.Fprintf(&b, "%s %s\n", icon[d.Level], d.Message)
		if d.Fix != "" && (d.Level == "error" || d.Level == "warn") {
			fmt.Fprintf(&b, "       -> %s\n", d.Fix)
		}
	}
	switch rep.Status {
	case "ok":
		b.WriteString("\nResult: everything checked is working.\n")
	case "warn":
		b.WriteString("\nResult: connected, with warnings (see above).\n")
	default:
		b.WriteString("\nResult: problems found. Fix the items marked [FAIL], then run this command again (or press Re-check in the control panel).\n")
	}
	return b.String()
}

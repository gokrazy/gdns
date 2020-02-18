// Command gdns sets up router7 dyndns entries for transparent proxies to make
// gokrazy processes available under a name instead of a port number.
//
// In other words, when installing the github.com/gokrazy/timestamps package on
// your gokrazy instance, you will be able to access it at
// http://timestamps.gokrazy/ in your browser.
//
// E.g.:
//  % gokr-packer -update=yes -hostname=gokrazy github.com/gokrazy/timestamps github.com/gokrazy/gdns
//  router7# mkdir /perm/radvd
//  router7# echo '[{"IP":"fdf5:3606:2a21::","Mask":"//////////8AAAAAAAAAAA=="}]' > /perm/radvd/prefixes.json
//  router7# killall radvd
//  % curl http://timestamps.gokrazy/metrics
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rtr7/dyndns"
	"github.com/vishvananda/netlink"
	"golang.org/x/net/idna"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

// ulaPrefix is a RFC4193 unique local IPv6 unicast address prefix
const ulaPrefix = "fdf5:3606:2a21:0:" // prefix length /64

func mustParseCIDR(s string) *net.IPNet {
	_, net, err := net.ParseCIDR(s)
	if err != nil {
		log.Panic(err)
	}
	return net
}

var ipv6LinkLocal = mustParseCIDR("fe80::/10")

func last64(ip net.IP) string {
	b := ip.To16()
	// ba27:ebff:fe8a:e014
	return fmt.Sprintf("%x%x:%x%x:%x%x:%x%x",
		b[8], b[9],
		b[10], b[11],
		b[12], b[13],
		b[14], b[15])
}

func ethernetEUI64() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, i := range ifaces {
		if i.Flags&net.FlagUp != net.FlagUp {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			return "", err
		}

		for _, a := range addrs {
			ipaddr, _, err := net.ParseCIDR(a.String())
			if err != nil {
				return "", err
			}

			if ipv6LinkLocal.Contains(ipaddr) {
				return last64(ipaddr), nil
			}
		}
	}
	return "", fmt.Errorf("no ethernet interface found")
}

var listeners = make(map[string]bool)

func logic() error {
	eui, err := ethernetEUI64()
	if err != nil {
		return err
	}
	eui = eui[strings.IndexByte(eui, ':'):]

	addrs, err := listenaddrs(nil)
	if err != nil {
		return fmt.Errorf("listenaddrs: %v", err)
	}
	var eg errgroup.Group
	used := make(map[string]bool)
	for _, addr := range addrs {
		// Assumption: a port is owned by one program.
		// Assumption: each program listens on either:
		// [::] (public) or (at least)
		// [::1] (private)
		if !addr.Addr.Equal(net.IPv6loopback) &&
			!addr.Addr.Equal(net.IPv6unspecified) {
			continue
		}

		port := strconv.Itoa(int(addr.Port))
		proxyaddr := ulaPrefix + port + eui

		cmdline := filepath.Base(string(addr.Cmdline))
		if cmdline == "init" {
			continue
		}
		name, err := idna.ToASCII(cmdline)
		if err != nil {
			return err
		}
		{
			uniq := name
			for i := 2; used[uniq]; i++ {
				uniq = name + "." + strconv.Itoa(i)
			}
			name = uniq
		}
		used[name] = true
		log.Printf("  %v %v → %v",
			net.JoinHostPort(addr.Addr.String(), port),
			name,
			proxyaddr,
		)
		if listeners[proxyaddr] {
			continue // already set up
		}
		listeners[proxyaddr] = true

		{
			// add IP address to eth0 if not already present
			addr, err := netlink.ParseAddr(proxyaddr + "/48")
			if err != nil {
				return fmt.Errorf("netlink.ParseAddr(%v): %v", proxyaddr, err)
			}
			// Skip duplicate address detection so that we can immediately
			// listen on this address after configuring:
			addr.Flags |= unix.IFA_F_NODAD
			link, err := netlink.LinkByName("eth0") // TODO
			if err != nil {
				link, err = netlink.LinkByName("lan0") // TODO
				if err != nil {
					return fmt.Errorf("netlink.LinkByName(eth0): %v", err)
				}
			}
			if err := netlink.AddrReplace(link, addr); err != nil {
				return fmt.Errorf("netlink.AddrAdd(%v, %v): %v", link, addr, err)
			}
		}

		// Set up listener and serve HTTP:
		ln, err := net.Listen("tcp", net.JoinHostPort(proxyaddr, "80"))
		if err != nil {
			log.Print(err)
			continue
		}

		target, err := url.Parse("http://[::1]:" + port)
		if err != nil {
			log.Print(err)
			continue
		}

		localAddr, err := net.ResolveTCPAddr("tcp6", net.JoinHostPort(proxyaddr, "0"))
		if err != nil {
			log.Print(err)
			continue
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = &http.Transport{
			Dial: (&net.Dialer{
				LocalAddr: localAddr,
			}).Dial,
		}
		eg.Go(func() error {
			return http.Serve(ln, proxy)
		})

		if err := dyndns.SetSubname(name, net.ParseIP(proxyaddr)); err != nil {
			return fmt.Errorf("dyndns.SetSubname(%q, %v): %v", name, proxyaddr, err)
		}
	}
	return eg.Wait()
}

func main() {
	if err := logic(); err != nil {
		log.Fatal(err)
	}
}

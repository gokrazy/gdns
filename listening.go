package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

const (
	_ = iota
	TCP_ESTABLISHED
	TCP_SYN_SENT
	TCP_SYN_RECV
	TCP_FIN_WAIT1
	TCP_FIN_WAIT2
	TCP_TIME_WAIT
	TCP_CLOSE
	TCP_CLOSE_WAIT
	TCP_LAST_ACK
	TCP_LISTEN
	TCP_CLOSING /* now a valid state */
)

const SOCK_DIAG_BY_FAMILY = 20

type inetDiagSockid struct {
	IdiagSport  uint16
	IdiagDport  uint16
	IdiagSrc    [16]byte
	IdiagDst    [4]uint32
	IdiagIf     uint32
	IdiagCookie [2]uint32
}

type inetDiagReqV2 struct {
	SdiagFamily   uint8
	SdiagProtocol uint8
	IdiagExt      uint8
	Pad0          uint8
	IdiagStates   uint32
	Id            inetDiagSockid
}

type inetDiagMsg struct {
	IdiagFamily  uint8
	IdiagState   uint8
	IdiagTimer   uint8
	IdiagRetrans uint8
	Id           inetDiagSockid
	IdiagExpires uint32
	IdiagRqueue  uint32
	IdiagWqueue  uint32
	IdiagUid     uint32
	IdiagInode   uint32
}

type listenAddr struct {
	Addr    net.IP
	Port    uint16
	Inode   uint32
	Cmdline string
}

func listenaddrs(nlcfg *netlink.Config) ([]listenAddr, error) {
	c, err := netlink.Dial(unix.NETLINK_SOCK_DIAG, nlcfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	var addrs []listenAddr
	for _, family := range []uint8{ /*unix.AF_INET,*/ unix.AF_INET6} {
		dreq := inetDiagReqV2{
			SdiagFamily:   family,
			SdiagProtocol: unix.IPPROTO_TCP,
			IdiagStates:   1<<TCP_CLOSE | 1<<TCP_LISTEN,
		}
		var buf bytes.Buffer
		if err := binary.Write(&buf, binary.LittleEndian, &dreq); err != nil {
			return nil, fmt.Errorf("binary.Write: %v", err)
		}

		req := netlink.Message{
			Header: netlink.Header{
				Type:  netlink.HeaderType(SOCK_DIAG_BY_FAMILY),
				Flags: netlink.HeaderFlagsRequest | /*netlink.HeaderFlagsAcknowledge | */ netlink.HeaderFlagsDump,
			},
			Data: buf.Bytes(),
		}
		msgs, err := c.Execute(req)
		if err != nil {
			return nil, fmt.Errorf("netlink.Execute: %v", err)
		}
		for _, msg := range msgs {
			var diag inetDiagMsg
			if err := binary.Read(bytes.NewReader(msg.Data), binary.LittleEndian, &diag); err != nil {
				return nil, fmt.Errorf("binary.Read: %v", err)
			}
			//log.Printf("msg: %+v", msg)
			//log.Printf(" -> %+v", diag)
			lport := diag.Id.IdiagSport
			lport = uint16((lport&0xFF)<<8) | uint16((lport&0xFF00)>>8)
			// uid: diag.IdiagUid
			ip := diag.Id.IdiagSrc[:]
			if family == unix.AF_INET {
				ip = ip[:4]
			}
			//log.Printf("local port %v, inode %v, ip %v", lport, diag.IdiagInode, net.IP(ip))
			addrs = append(addrs, listenAddr{
				Addr:  net.IP(ip),
				Port:  lport,
				Inode: diag.IdiagInode,
			})
		}
	}

	byTarget := make(map[string]*listenAddr, len(addrs))
	for idx, addr := range addrs {
		byTarget[fmt.Sprintf("socket:[%d]", addr.Inode)] = &addrs[idx]
	}

	fis, err := ioutil.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		if !fi.Mode().IsDir() {
			continue
		}
		cmdline, err := ioutil.ReadFile(filepath.Join("/proc", fi.Name(), "cmdline"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if len(cmdline) == 0 {
			continue
		}
		if idx := bytes.IndexByte(cmdline, '\x00'); idx > -1 {
			cmdline = cmdline[:idx]
		}

		fddir := filepath.Join("/proc", fi.Name(), "fd")
		fdfis, err := ioutil.ReadDir(fddir)
		if err != nil {
			continue // TODO: only on permission denied
			return nil, err
		}
		for _, fdfi := range fdfis {
			if fdfi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(filepath.Join(fddir, fdfi.Name()))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			addr, ok := byTarget[target]
			if !ok {
				continue
			}
			addr.Cmdline = string(cmdline)
		}
	}

	return addrs, nil
}

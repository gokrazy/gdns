[![Build Status](https://travis-ci.org/gokrazy/gdns.svg?branch=master)](https://travis-ci.org/gokrazy/gdns)
[![Go Report Card](https://goreportcard.com/badge/github.com/gokrazy/gdns)](https://goreportcard.com/report/github.com/gokrazy/gdns)

# Overview

gdns sets up [router7](https://github.com/rtr7/router7) dyndns entries for
transparent proxies to make gokrazy processes available under a name instead of
a port number.

In other words, when installing the `github.com/gokrazy/timestamps` package on
your gokrazy instance, you will be able to access it at
http://timestamps.gokrazy/ in your browser.

# Usage

First, configure the `fdf5:3606:2a21::/64` IPv6 network which gdns uses:

```
router7# mkdir /perm/radvd
router7# echo '[{"IP":"fdf5:3606:2a21::","Mask":"//////////8AAAAAAAAAAA=="}]' > /perm/radvd/prefixes.json
router7# killall radvd
```

Then, include gdns in your gokrazy installation(s):

```
% gokr-packer -update=yes -hostname=gokrazy github.com/gokrazy/timestamps github.com/gokrazy/gdns
% sleep 20
% curl http://timestamps.gokrazy/metrics
```

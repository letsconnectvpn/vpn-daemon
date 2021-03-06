# VPN Daemon

VPN Deemon is a simple daemon that provides a TCP socket API protected by TLS 
as an abstraction on top of the management port of (multiple) OpenVPN server 
process(es). The API exposes functionality to retrieve a list of connected VPN 
clients and also allows for disconnecting currently connected clients.

## Why?

On a VPN server we need to manage multiple OpenVPN processes. Each OpenVPN 
process exposes it management interface through a (TCP) socket. This works fine 
when the OpenVPN processes and the VPN controller run on the same machine. If 
both the controller and OpenVPN processes run on different hosts this is not 
secure as there is no TLS. Furthermore, it is inefficient, i.e. we have to 
query all OpenVPN management ports over the network for all hosts.

Currently, when using multiple hosts, one MUST have a secure channel between
the controller and node(s), which is something we do not want to require. A 
simple TLS channel protected by client certificate authentication over the open 
Internet should be enough...

This daemon will provide the exact same functionality as the current situation,
except the portal will talk to only one socket per VPN node, protected using 
TLS.

## How?

What we want to build is a simple daemon that runs on the same system as the 
OpenVPN processes and is reachable over a TCP socket protected by TLS. The 
daemon will then take care of contacting the OpenVPN processes through their 
local management ports and execute the commands. We want to make this 
"configuration-less", i.e. the daemon should require no additional 
configuration to make integrating it in the current system as easy as possible.

Currently there are two commands used over the OpenVPN management connection: 
`status` and `kill` where `status` returns a list of connected clients, and 
`kill` disconnects a client.

In a default installation our VPN server has two OpenVPN processes, so the 
daemon will need to talk to both OpenVPN processes. The portal can just talk to 
the daemon and issues a command there. The results will be merged by the 
daemon.

Furthermore, we can simplify the API used to retrieve the list of connected 
clients and disconnect clients. We will only expose what we explicitly use 
and need, nothing more.

## Architecture

                            .-------------.
                            | Controller  |
                            | (Portal)    |
                            '-------------'
                                   |
                            TCP+TLS Socket
                                   |
                                   v
                             .-----------.
                             | Daemon    |
                       .-----|           |------.
                       |     |           |      |
                       |     '-----------'      |
                       |                        |
                 TCP Socket                TCP Socket
                       |                        |
                       |                        |
                       v                        v
                 .---------.               .---------.
                 | OpenVPN |               | OpenVPN |
                 '---------'               '---------'

## Benefits

The daemon will be written in Go, which can handle connections to the OpenVPN
management port concurrently. It doesn't have to perform the request one after 
the other as is currently the case. This may improve performance.

We can use TLS with the daemon and require TLS client certificate 
authentication. 

The parsing of the OpenVPN "legacy" protocol and merging of the 
information can be done by the daemon simplifying the implementation of the 
controller.

We can also begin to envision implementing other VPN protocols when we have
a control daemon, e.g. WireGuard. The daemon would need to have additional 
commands then, i.e. `setup` and `teardown`.

## API

### Command / Response

Currently _n_ commands are implemented:

* `SET_PORTS`
* `DISCONNECT`
* `LIST`
* `SETUP`
* `CLIENT_CONNECT`
* `CLIENT_DISCONNECT`
* `LOG`
* `QUIT`

The commands are given, some with parameters, and the response will be of the 
format:
    
    OK: n

Where `n` is the number of rows the response contains. This is an integer >= 0. 
See the examples below.

If a command is not supported, malformed, or a command fails, the response 
starts with `ERR`, e.g.:

    FOO
    ERR: NOT_SUPPORTED

### Setup

As we want to go for "zero configuration", we want the controller to specify 
which OpenVPN management ports we want to talk to.

Example:

    SET_PORTS 11940 11941

Response:

    OK: 0

This works well for single profile VPN servers, but if there are multiple 
profiles involved, one has to specify them all in case of `DISCONNECT`, and 
a subset (just the ones for the profile one is interested in) when calling 
`LIST`.

### Disconnect 

`DISCONNECT` will disconnect the mentioned CN(s).

    DISCONNECT <CN_1> <CN_2> ... <CN_n>

Example:

    DISCONNECT 07d1ccc455a21c2d5ac6068d4af727ca
    
Response:

    OK: 0

### List

This will list all currently connected clients to the configured OpenVPN 
management ports. It exposes the CN and the IPv4 and IPv6 address assigned
to the VPN client.

Example:

    LIST

Response:

    OK: 2
    07d1ccc455a21c2d5ac6068d4af727ca 10.42.42.2 fd00:4242:4242:4242::1000
    9b8acc27bec2d5beb06c78bcd464d042 10.132.193.3 fd0b:7113:df63:d03c::1001

### Setup

This will tell the node which profiles are available for a certain CN. 

Example:

    SETUP 07d1ccc455a21c2d5ac6068d4af727ca profile1 profile2

Response:
    
    OK: 0

The example above tells the node that the certificate with CN 
`07d1ccc455a21c2d5ac6068d4af727ca` has access to the profiles `profile1` and 
`profile2`.

This generates a file in 
`/var/lib/vpn-daemon/c/07d1ccc455a21c2d5ac6068d4af727ca` with the content 
`profile1 profile2`. This will be reviewed by the `CLIENT_CONNECT` call to make 
sure the CN is allowed to use the profile it wants to use.

As the CN is bound to a certificate that expires, we do not need to record
when this particular CN is no longer allowed to connect.

**TODO**: how to delete the CN? separate `DELETE` command? Should it 
be combined with `DISCONNECT`? Should we introduce JSON(-like) syntax to allow
specifying empty array?

### Client Connect

Example:

    CLIENT_CONNECT profile1 9b8acc27bec2d5beb06c78bcd464d042 10.42.42.42 fd00:4242:4242:4242::1000

Response:

    OK: 0

**TODO**: we need to also get the `time_unix` from the environment on client 
disconnect as to find the exact log file to write to and be able to match it 
to disconnect...

### Client Disconnect

    CLIENT_DISCONNECT profile1 9b8acc27bec2d5beb06c78bcd464d042 10.42.42.42 fd00:4242:4242:4242::1000

Response: 

    OK: 0

**TODO**: we need to also get the `time_unix` from the environment on client 
disconnect as to find the correct log file to write to...

### Log

In order to obtain the log from a node, the LOG call is introduced:

Example:

    LOG 10.42.42.42 2019-01-01T08:00:00+00:00

This would return (if there is a log entry) which CN was connected to the VPN 
using this IP at the provided time.

Response:

    OK: 1
    profile1 2019-01-01T08:00:00+00:00 2019-01-01T10:00:00+00:00 9b8acc27bec2d5beb06c78bcd464d042

The log file is obtained from the file system. Log files are created by the
`CLIENT_CONNECT` and `CLIENT_DISCONNECT` call to the daemon from the OpenVPN 
`--client-connect` and `--client-disconnect` scripts.

The log files are stored in `/var/log/vpn-daemon`, for example:

    /var/log/vpn-daemon/10.42.42.42/2019-01-01T08:00:00+00:00

This file is created by `CLIENT_CONNECT` after which it contains the profile 
ID, e.g.:

    profile1

On `CLIENT_DISCONNECT` in addition the disconnect time and the total number 
of bytes will be added to it, e.g.:

    profile1 2019-01-01T10:00:00+00:00 10485760

The daemon needs to be smart enough to find the correct file to look in when 
the query comes in. The specified time MUST be between the time 
specified in the file name and the time of disconnect mentioned in the file. It 
is also possible no disconnect info available (yet), for example the client is
still connected, or the OpenVPN process crashed.

A cleanup script MUST remove the log files after a specified time frame has 
passed, e.g. 30 days.

**TODO**: clean up this mess :-) There must be a slightly better way?
**TODO**: write more about client still connected and crashing OpenVPN process
**TODO**: log also needs to contain the profile ID, and also MUST return this,
e.g. profile,CN
**TODO**: we can have separate log files for IPv4 and IPv6 if that helps, 
probably...
**TODO**: could something like logrotate take care of the log file removal?
**TODO**: the formats are getting a bit more tricky, this almost asks for JSON,
resist as long as possible ;-)

### Quit

To close the connection:

    QUIT

## Build & Run

    $ go build -o _bin/vpn-daemon vpn-daemon/main.go

Or use the `Makefile`:

	$ make

## Run

    $ _bin/vpn-daemon

One can then `telnet` to port `41194`, and issue commands:

    $ telnet localhost 41194
    Trying ::1...
    Connected to localhost.
    Escape character is '^]'.
    SET_PORTS 11940 11941
    OK: 0
    DISCONNECT foo
    OK: 0
    QUIT

By default the daemon listens on `localhost:41194`. If you want to modify this
you can specify the `-listen` option to change this, e.g.:

    $ _bin/vpn-daemon -listen 192.168.122.1:41194

### TLS 

We use [vpn-ca](https://git.tuxed.net/LC/vpn-ca) to generate a CA:
    
    $ vpn-ca -init
    $ vpn-ca -server server
    $ vpn-ca -client client

If you want to enable TLS, i.e. require clients to connect over TLS, start 
the daemon with the `-enable-tls` flag, e.g.

    $ _bin/vpn-daemon -enable-tls

If you want to change the path where the CA, certificate and key are located, 
you can recompile the daemon with the flags `tlsCertDir` and `tlsKeyDir`, e.g.:

    $ go build -o _bin/vpn-daemon -ldflags="-X main.tlsCertDir=/path/to/cert -X main.tlsKeyDir=/path/to/key" vpn-daemon/main.go

In order to test the connection, you can use `openssl` and use it as a "telnet" 
to interact with the daemon:

    $ openssl s_client -connect 127.0.0.1:41194 -cert client.crt -key client.key -CAfile ca.crt

## Test

To run the test suite:

    $ make test

/*
 * Simple code to talk to the OpenVPN management ports of multiple OpenVPN
 * processes. This code will open sockets, send "kill" command and agreggate
 * the number of disconnected clients.
 *
 * A telnet session to a single OpenVPN process looks like this:
 *
 * [fkooman@vpn ~]$ telnet localhost 11940
 * Trying ::1...
 * telnet: connect to address ::1: Connection refused
 * Trying 127.0.0.1...
 * Connected to localhost.
 * Escape character is '^]'.
 * >INFO:OpenVPN Management Interface Version 1 -- type 'help' for more info
 * kill 07d1ccc455a21c2d5ac6068d4af727ca
 * SUCCESS: common name '07d1ccc455a21c2d5ac6068d4af727ca' found, 1 client(s) killed
 * kill foo
 * ERROR: common name 'foo' not found
 * quit
 * Connection closed by foreign host.
 * [fkooman@vpn ~]$
 *
 * The point here is to be able to (concurrently) connect to many OpenVPN
 * processes. The example below has only two. Extra functionality later will
 * be also the use of the "status" command to see which clients are connected
 * and aggregate that as well.
 *
 * Eventually this will need to become a daemon that supports TLS and abstracts
 * the multiple OpenVPN processes away from the daemon caller...
 */
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

//struct used in LIST
type connectionInfo struct {
	commonName  string
	virtualIPv4 string
	virtualIPv6 string
}

func main() {
	var listenHostPort = flag.String("listen", "localhost:41194", "IP:port to listen on")
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	flag.Parse()
	ln, err := net.Listen("tcp", *listenHostPort)
	if err != nil {
		// handle error
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			// handle error
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	intPortList := make([]int, 0)

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		msg, _ := reader.ReadString('\n')
		if 0 == strings.Index(msg, "SET_OPENVPN_MANAGEMENT_PORT_LIST") {
			newPortList, err := parsePortCommand(msg)
			if err == nil {
				intPortList = newPortList
				writer.WriteString(fmt.Sprintf("OK: 0\n"))
				writer.Flush()
				continue
			}

			writer.WriteString(fmt.Sprintf("ERR: %s\n", err))
			writer.Flush()
			continue
		}

		if 0 == strings.Index(msg, "DISCONNECT") {
			if len(msg) > 13 {
				// we don't want to \n otherwise we could use msg[11:]
				commonName := msg[11 : len(msg)-2]

				//parsing commonName
				validCommonName := regexp.MustCompile(`^[a-zA-Z0-9-.]+$`)
				if !validCommonName.MatchString(commonName) {
					writer.WriteString(fmt.Sprintf("ERR: INVALID_PARAMETER\n"))
					writer.Flush()
					continue
				}

				c := make(chan bool, len(intPortList))
				var wgDisc sync.WaitGroup

				for _, p := range intPortList {
					wgDisc.Add(1)
					go disconnectClient(c, p, commonName, &wgDisc)
				}

				// wait for all routines to finish...
				wgDisc.Wait()

				// close channel, we do not expect any data anymore, this is needed
				// because otherwise "range c" below is still waiting for more data on the
				// channel...
				close(c)

				// below we basically count all the "trues" in the channel populated by the
				// routines...
				clientDisconnectCount := 0
				for b := range c {
					if b {
						clientDisconnectCount++
					}
				}

				writer.WriteString(fmt.Sprintf("OK: 1\n"))
				writer.WriteString(fmt.Sprintf("%d\n", clientDisconnectCount))
				writer.Flush()
			} else {
				writer.WriteString(fmt.Sprintf("ERR: MISSING_PARAMETER\n"))
				writer.Flush()
			}

			continue
		}

		if 0 == strings.Index(msg, "LIST") {

			//prevent connection getting stuck, wait for next line
			if len(intPortList) == 0 {
				writer.WriteString(fmt.Sprintf("OK: 0\n"))
				writer.Flush()
				continue
			}

			c := make(chan []*connectionInfo, len(intPortList))
			var wgList sync.WaitGroup

			for _, p := range intPortList {
				wgList.Add(1)
				go obtainStatus(c, p, &wgList)
			}

			// wait for all routines to finish...
			wgList.Wait()

			// close channel, we do not expect any data anymore, this is needed
			// because otherwise "range c" below is still waiting for more data on the
			// channel...
			close(c)

			connectionCount := 0
			rtnConnList := make([]string, 0)
			for x := range c {
				if x != nil {
					for _, connection := range x {
						connectionCount++
						rtnConnList = append(rtnConnList, fmt.Sprintf("%s %s %s", connection.commonName, connection.virtualIPv4, connection.virtualIPv6))
					}
				}
			}

			writer.WriteString(fmt.Sprintf("OK: %d\n", connectionCount))
			for _, val := range rtnConnList {
				writer.WriteString(fmt.Sprintf("%s\n", val))
			}
			writer.Flush()

			continue
		}

		if 0 == strings.Index(msg, "QUIT") {
			return
		}

		writer.WriteString(fmt.Sprintf("ERR: NOT_SUPPORTED\n"))
		writer.Flush()
	}
}

func disconnectClient(c chan bool, p int, commonName string, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		// unable to connect, no matter, maybe the process is temporary away,
		// so no need to disconnect clients there ;-)
		c <- false
		return
	}

	defer conn.Close()

	// turn off live OpenVPN log that can confuse our output parsing
	fmt.Fprintf(conn, fmt.Sprint("log off\n"))

	reader := bufio.NewReader(conn)
	// we need to remove everything that's currently in the buffer waiting to
	// be read. We are not interested in it at all, we only care about the
	// response to our commands hereafter...
	// XXX there should be a one-liner that can fix this, right?
	txt, _ := reader.ReadString('\n')
	for 0 != strings.Index(txt, "END") && 0 != strings.Index(txt, "SUCCESS") && 0 != strings.Index(txt, "ERROR") {
		txt, _ = reader.ReadString('\n')
	}

	// disconnect the client
	fmt.Fprintf(conn, fmt.Sprintf("kill %s\n", commonName))
	text, _ := reader.ReadString('\n')
	if 0 == strings.Index(text, "SUCCESS") {
		c <- true
	} else {
		c <- false
	}

	// XXX maybe it is easier to just close the connection, who cares about
	// quit?
	fmt.Fprintf(conn, "quit\n")
}

func obtainStatus(c chan []*connectionInfo, p int, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		// unable to connect, no matter, maybe the process is temporary away,
		// so no need to retrieve clients there ;-)
		c <- nil
		return
	}

	defer conn.Close()

	reader := bufio.NewReader(conn)

	// send status command to OpenVPN management interface
	fmt.Fprintf(conn, "status 2\n")
	text, _ := reader.ReadString('\n')
	for 0 != strings.Index(text, "CLIENT_LIST") {
		// walk until we find CLIENT_LIST
		// exit loop if no clients are found -> if not inf loop searching for "CLIENT_LIST"
		if 0 == strings.Index(text, "END") {
			break
		}
		text, _ = reader.ReadString('\n')
	}

	connections := make([]*connectionInfo, 0)
	//can continue to iterate through the msg till END is found
	//can continue even when interleaving msgs are present
	for 0 != strings.Index(text, "END") {
		if 0 == strings.Index(text, "CLIENT_LIST") {
			strList := strings.Split(text, ",")
			//x[0] = "CLIENT_LIST"				x[1] = {COMMON NAME}				x[2] = {Real Address}
			//x[3] = {Virtual IPv4 Address}		x[4] = {Virtual IPv6 Address}		x[5] = {Bytes Received}
			//x[6] = {Bytes Sent}				x[7] = {Connected Since}			x[8] = {Connetected Since (time_t)}
			//x[9] = {Username}					x[10]= {Client ID}					x[11]= {Peer ID}
			newConnection := connectionInfo{strList[1], strList[3], strList[4]}
			connections = append(connections, &newConnection)
		}
		text, _ = reader.ReadString('\n')
	}

	c <- connections
}

func parsePortCommand(msg string) ([]int, error) {
	if len(msg) <= 35 {
		return nil, errors.New("MISSING_PARAMETER")
	}

	portList := strings.Fields(msg[33 : len(msg)-2])
	newPortList := make([]int, 0)
	if len(portList) == 0 {
		return newPortList, errors.New("MISSING_PARAMETER")
	}

	for _, port := range portList {
		intPort, err := strconv.Atoi(port)
		if err != nil || intPort <= 0 || intPort >= 65536 {
			return newPortList, errors.New("INVALID_PARAMETER")
		}
		newPortList = append(newPortList, intPort)
	}

	return newPortList, nil
}

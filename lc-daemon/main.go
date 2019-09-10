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

import "net"
import "fmt"
import "bufio"
import "strings"
import "sync"
import "strconv"

var wg sync.WaitGroup

func main() {

	ln, err := net.Listen("tcp", ":8080")
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

	var commonName string
	intPortList := make([]int, 0)

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		msg, _ := reader.ReadString('\n')
		if 0 == strings.Index(msg, "SET_OPENVPN_MANAGEMENT_PORT_LIST") {
			portList := strings.Split(msg[33:len(msg)-2], " ")
			for _, port := range portList {
				intPort, _ := strconv.Atoi(port)
				intPortList = append(intPortList, intPort)
			}
		}
		if 0 == strings.Index(msg, "DISCONNECT") {
			if len(msg) > 13 {
				// we don't want to \n otherwise we could use msg[11:]
				commonName = msg[11 : len(msg)-2]

				c := make(chan bool, len(intPortList))

				for _, p := range intPortList {
					wg.Add(1)
					go disconnectClient(c, p, commonName)
				}

				// wait for all routines to finish...
				wg.Wait()

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

				writer.WriteString(fmt.Sprintf("%d\n", clientDisconnectCount))
				writer.Flush()
			}
		}

		if 0 == strings.Index(msg, "LIST") {

			fmt.Println("LIST")

			c := make(chan []string, len(intPortList))

			for _, p := range intPortList {
				wg.Add(1)
				go obtainStatus(c, p)
			}

			// wait for all routines to finish...
			wg.Wait()

			// close channel, we do not expect any data anymore, this is needed
			// because otherwise "range c" below is still waiting for more data on the
			// channel...
			close(c)

			for x := range c {
				fmt.Println(x)
			}
		}

		if 0 == strings.Index(msg, "QUIT") {
			return
		}
	}
}

func disconnectClient(c chan bool, p int, commonName string) {
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

func obtainStatus(c chan []string, p int) {
	defer wg.Done()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		// unable to connect, no matter, maybe the process is temporary away,
		// so no need to disconnect clients there ;-)
		c <- nil
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
	fmt.Fprintf(conn, "status 2\n")
	text, _ := reader.ReadString('\n')
	for 0 != strings.Index(text, "CLIENT_LIST") {
		// walk until we find CLIENT_LIST
		text, _ = reader.ReadString('\n')
	}

	for 0 == strings.Index(text, "CLIENT_LIST") {
		strList := strings.Split(text, ",")
		c <- strList
	}

	// XXX maybe it is easier to just close the connection, who cares about
	// quit?
	fmt.Fprintf(conn, "quit\n")
}
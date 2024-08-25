package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func main() {
	serverAddr := "0.0.0.0:2222"
	folder := "/app/sky"

	//ssh config
	sshConfig := &ssh.ServerConfig{
		//password based auth
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == "password" {
				return &ssh.Permissions{}, nil
			}

			return nil, fmt.Errorf("password rejected for %q", c.User())
		},
	}

	keyBytes, err := os.ReadFile("id_rsa")
	if err != nil {
		log.Fatal("Failed to load private key: ", err)
	}

	key, err := ssh.ParsePrivateKey(keyBytes)

	sshConfig.AddHostKey(key)

	//sftp server
	listener, err := net.Listen("tcp", serverAddr)
	if err != nil {
		log.Fatal("failed to listen ", serverAddr)
	}

	fmt.Println("running on ", serverAddr)

	//handle connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal("failed to accept conn: ", err)
		}

		go handleConnection(conn, sshConfig, folder)
	}
}

func handleConnection(conn net.Conn, config *ssh.ServerConfig, folder string) {
	//ssh handshake
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		log.Println("handshake failed:", err)
		return
	}
	defer sshConn.Close() //close connection properly

	log.Println("connection from", sshConn.RemoteAddr(), string(sshConn.ClientVersion()))

	//discard global reqs
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
		}

		session, sessionReqs, err := newChannel.Accept()
		if err != nil {
			log.Fatal("error accepting channel", err)
			continue
		}
		defer session.Close() //when this stops, close the session

		//handle session channel requests
		go func(in <-chan *ssh.Request) {
			for req := range sessionReqs {
				ok := false
				switch req.Type {
				case "subsystem":
					if string(req.Payload[4:]) == "sftp" {
						ok = true
					}
				}
				req.Reply(ok, nil)
			}
		}(sessionReqs)

		//create server for the connection
		server, err := sftp.NewServer(
			session,
			sftp.WithDebug(os.Stderr),
			sftp.WithServerWorkingDirectory(folder),
		)

		if err != nil {
			log.Fatal("failed to create sftp server", err)
			continue
		}

		err = server.Serve()
		if err != nil {
			if err == io.EOF {
				log.Println("sftp client disconnected")
				server.Close()
			}

			log.Fatal("failed to serve sftp server", err)
			server.Close()
		}

		//Close session when server finishes
		session.Close()
	}
}

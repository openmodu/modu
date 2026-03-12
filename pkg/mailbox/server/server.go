package server

import (
	"log"
	"strings"

	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/tidwall/redcon"
)

type MailboxServer struct {
	hub *mailbox.Hub
}

func NewMailboxServer() *MailboxServer {
	return &MailboxServer{
		hub: mailbox.NewHub(),
	}
}

func (s *MailboxServer) ListenAndServe(addr string) error {
	log.Printf("Mailbox Server starting on %s...", addr)

	return redcon.ListenAndServe(addr,
		func(conn redcon.Conn, cmd redcon.Command) {
			// 指令路由
			switch strings.ToUpper(string(cmd.Args[0])) {
			default:
				conn.WriteError("ERR unknown command '" + string(cmd.Args[0]) + "'")

			case "PING":
				conn.WriteString("PONG")

			case "AGENT.PING": // AGENT.PING <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.PING' command")
					return
				}
				agentID := string(cmd.Args[1])
				if err := s.hub.Heartbeat(agentID); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("PONG")
				}

			case "AGENT.LIST": // AGENT.LIST
				if len(cmd.Args) != 1 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.LIST' command")
					return
				}
				agents := s.hub.ListAgents()
				conn.WriteArray(len(agents))
				for _, a := range agents {
					conn.WriteBulkString(a)
				}

			case "AGENT.REG": // AGENT.REG <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.REG' command")
					return
				}
				agentID := string(cmd.Args[1])
				s.hub.Register(agentID)
				conn.WriteString("OK")

			case "MSG.SEND": // MSG.SEND <target_id> <message>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'MSG.SEND' command")
					return
				}
				targetID := string(cmd.Args[1])
				msg := string(cmd.Args[2])

				if err := s.hub.Send(targetID, msg); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "MSG.RECV": // MSG.RECV <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'MSG.RECV' command")
					return
				}
				agentID := string(cmd.Args[1])
				msg, hasMsg := s.hub.Recv(agentID)

				if hasMsg {
					conn.WriteBulkString(msg)
				} else {
					conn.WriteNull() // 对应 Redis 的 nil 响应
				}

			case "MSG.BCAST": // MSG.BCAST <message>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'MSG.BCAST' command")
					return
				}
				msg := string(cmd.Args[1])
				s.hub.Broadcast(msg)
				conn.WriteString("OK")
			}
		},
		func(conn redcon.Conn) bool { return true },
		func(conn redcon.Conn, err error) {},
	)
}

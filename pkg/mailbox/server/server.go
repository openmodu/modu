package server

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/tidwall/redcon"
)

type MailboxServer struct {
	hub *mailbox.Hub
}

func NewMailboxServer(opts ...mailbox.HubOption) *MailboxServer {
	return &MailboxServer{
		hub: mailbox.NewHub(opts...),
	}
}

// Hub 返回内部 Hub 引用，供 Dashboard 等外部组件订阅事件
func (s *MailboxServer) Hub() *mailbox.Hub {
	return s.hub
}

func (s *MailboxServer) ListenAndServe(addr string) error {
	log.Printf("Mailbox Server starting on %s...", addr)

	return redcon.ListenAndServe(addr,
		func(conn redcon.Conn, cmd redcon.Command) {
			switch strings.ToUpper(string(cmd.Args[0])) {
			default:
				conn.WriteError("ERR unknown command '" + string(cmd.Args[0]) + "'")

			case "PING":
				conn.WriteString("PONG")

			// --- Agent 基础命令 ---

			case "AGENT.REG": // AGENT.REG <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.REG' command")
					return
				}
				s.hub.Register(string(cmd.Args[1]))
				conn.WriteString("OK")

			case "AGENT.PING": // AGENT.PING <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.PING' command")
					return
				}
				if err := s.hub.Heartbeat(string(cmd.Args[1])); err != nil {
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

			// --- Agent 元数据命令 ---

			case "AGENT.SETROLE": // AGENT.SETROLE <agent_id> <role>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.SETROLE' command")
					return
				}
				if err := s.hub.SetAgentRole(string(cmd.Args[1]), string(cmd.Args[2])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "AGENT.SETSTATUS": // AGENT.SETSTATUS <agent_id> <status> [task_id]
				if len(cmd.Args) < 3 || len(cmd.Args) > 4 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.SETSTATUS' command")
					return
				}
				taskID := ""
				if len(cmd.Args) == 4 {
					taskID = string(cmd.Args[3])
				}
				if err := s.hub.SetAgentStatus(string(cmd.Args[1]), string(cmd.Args[2]), taskID); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "AGENT.INFO": // AGENT.INFO <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.INFO' command")
					return
				}
				info, err := s.hub.GetAgentInfo(string(cmd.Args[1]))
				if err != nil {
					conn.WriteError("ERR " + err.Error())
					return
				}
				b, err := json.Marshal(info)
				if err != nil {
					conn.WriteError("ERR failed to serialize agent info")
					return
				}
				conn.WriteBulkString(string(b))

			// --- 消息命令 ---

			case "MSG.SEND": // MSG.SEND <target_id> <message>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'MSG.SEND' command")
					return
				}
				if err := s.hub.Send(string(cmd.Args[1]), string(cmd.Args[2])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "MSG.RECV": // MSG.RECV <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'MSG.RECV' command")
					return
				}
				msg, hasMsg := s.hub.Recv(string(cmd.Args[1]))
				if hasMsg {
					conn.WriteBulkString(msg)
				} else {
					conn.WriteNull()
				}

			case "MSG.BCAST": // MSG.BCAST <message>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'MSG.BCAST' command")
					return
				}
				s.hub.Broadcast(string(cmd.Args[1]))
				conn.WriteString("OK")

			// --- Task 命令 ---

			case "TASK.CREATE": // TASK.CREATE <creator_id> <description>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.CREATE' command")
					return
				}
				taskID, err := s.hub.CreateTask(string(cmd.Args[1]), string(cmd.Args[2]))
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(taskID)
				}

			case "TASK.ASSIGN": // TASK.ASSIGN <task_id> <agent_id>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.ASSIGN' command")
					return
				}
				if err := s.hub.AssignTask(string(cmd.Args[1]), string(cmd.Args[2])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "TASK.START": // TASK.START <task_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.START' command")
					return
				}
				if err := s.hub.StartTask(string(cmd.Args[1])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "TASK.DONE": // TASK.DONE <task_id> <result>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.DONE' command")
					return
				}
				if err := s.hub.CompleteTask(string(cmd.Args[1]), string(cmd.Args[2])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "TASK.FAIL": // TASK.FAIL <task_id> <error>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.FAIL' command")
					return
				}
				if err := s.hub.FailTask(string(cmd.Args[1]), string(cmd.Args[2])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "TASK.LIST": // TASK.LIST
				if len(cmd.Args) != 1 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.LIST' command")
					return
				}
				tasks := s.hub.ListTasks()
				b, err := json.Marshal(tasks)
				if err != nil {
					conn.WriteError("ERR failed to serialize tasks")
					return
				}
				conn.WriteBulkString(string(b))

			case "TASK.GET": // TASK.GET <task_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.GET' command")
					return
				}
				task, err := s.hub.GetTask(string(cmd.Args[1]))
				if err != nil {
					conn.WriteError("ERR " + err.Error())
					return
				}
				b, err := json.Marshal(task)
				if err != nil {
					conn.WriteError("ERR failed to serialize task")
					return
				}
				conn.WriteBulkString(string(b))
			}
		},
		func(conn redcon.Conn) bool { return true },
		func(conn redcon.Conn, err error) {},
	)
}

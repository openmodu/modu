package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/openmodu/modu/pkg/mailbox"
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

			case "AGENT.SETCAPS": // AGENT.SETCAPS <agent_id> <cap1> [cap2 ...]
				if len(cmd.Args) < 3 {
					conn.WriteError("ERR wrong number of arguments for 'AGENT.SETCAPS' command")
					return
				}
				caps := make([]string, 0, len(cmd.Args)-2)
				for _, a := range cmd.Args[2:] {
					caps = append(caps, string(a))
				}
				if err := s.hub.SetCapabilities(string(cmd.Args[1]), caps); err != nil {
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

			// --- Swarm 命令 ---

			case "TASK.PUBLISH": // TASK.PUBLISH <creator_id> <description> [cap1 cap2 ...]
				if len(cmd.Args) < 3 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.PUBLISH' command")
					return
				}
				caps := make([]string, 0, len(cmd.Args)-3)
				for _, a := range cmd.Args[3:] {
					caps = append(caps, string(a))
				}
				taskID, err := s.hub.PublishTask(string(cmd.Args[1]), string(cmd.Args[2]), caps...)
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(taskID)
				}

			case "TASK.CLAIM": // TASK.CLAIM <agent_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.CLAIM' command")
					return
				}
				task, ok := s.hub.ClaimTask(string(cmd.Args[1]))
				if !ok {
					conn.WriteNull()
					return
				}
				b, err := json.Marshal(task)
				if err != nil {
					conn.WriteError("ERR failed to serialize task")
					return
				}
				conn.WriteBulkString(string(b))

			case "TASK.QUEUE": // TASK.QUEUE
				if len(cmd.Args) != 1 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.QUEUE' command")
					return
				}
				tasks := s.hub.ListSwarmQueue()
				b, err := json.Marshal(tasks)
				if err != nil {
					conn.WriteError("ERR failed to serialize swarm queue")
					return
				}
				conn.WriteBulkString(string(b))

			// --- Adversarial validation commands ---

			case "TASK.PUBLISH_VALIDATED": // TASK.PUBLISH_VALIDATED <creator_id> <description> <max_retries> <pass_threshold> [cap...]
				if len(cmd.Args) < 5 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.PUBLISH_VALIDATED' command")
					return
				}
				var maxRetries int
				fmt.Sscanf(string(cmd.Args[3]), "%d", &maxRetries)
				var passThreshold float64
				fmt.Sscanf(string(cmd.Args[4]), "%f", &passThreshold)
				caps := make([]string, 0, len(cmd.Args)-5)
				for _, a := range cmd.Args[5:] {
					caps = append(caps, string(a))
				}
				taskID, err := s.hub.PublishValidatedTask(string(cmd.Args[1]), string(cmd.Args[2]), maxRetries, passThreshold, caps...)
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(taskID)
				}

			case "TASK.SUBMIT": // TASK.SUBMIT <task_id> <agent_id> <result>  → validate_task_id
				if len(cmd.Args) != 4 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.SUBMIT' command")
					return
				}
				validateTaskID, err := s.hub.SubmitForValidation(string(cmd.Args[1]), string(cmd.Args[2]), string(cmd.Args[3]))
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(validateTaskID)
				}

			case "TASK.VALIDATE": // TASK.VALIDATE <validate_task_id> <validator_id> <score> <feedback>
				if len(cmd.Args) != 5 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.VALIDATE' command")
					return
				}
				var score float64
				if _, err := fmt.Sscanf(string(cmd.Args[3]), "%f", &score); err != nil {
					conn.WriteError("ERR invalid score: must be a float between 0.0 and 1.0")
					return
				}
				if err := s.hub.SubmitValidation(string(cmd.Args[1]), string(cmd.Args[2]), score, string(cmd.Args[4])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "TASK.CREATE": // TASK.CREATE <creator_id> <description> [project_id]
				if len(cmd.Args) < 3 || len(cmd.Args) > 4 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.CREATE' command")
					return
				}
				var taskID string
				var err error
				if len(cmd.Args) == 4 {
					taskID, err = s.hub.CreateTask(string(cmd.Args[1]), string(cmd.Args[2]), string(cmd.Args[3]))
				} else {
					taskID, err = s.hub.CreateTask(string(cmd.Args[1]), string(cmd.Args[2]))
				}
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(taskID)
				}

			case "TASK.ASSIGN": // TASK.ASSIGN <task_id> <agent_id> [agent_id...]
				if len(cmd.Args) < 3 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.ASSIGN' command")
					return
				}
				taskID := string(cmd.Args[1])
				for i := 2; i < len(cmd.Args); i++ {
					if err := s.hub.AssignTask(taskID, string(cmd.Args[i])); err != nil {
						conn.WriteError("ERR " + err.Error())
						return
					}
				}
				conn.WriteString("OK")

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

			case "TASK.DONE":
				// TASK.DONE <task_id> <result>            (legacy, 3 args)
				// TASK.DONE <task_id> <agent_id> <result> (new, 4 args)
				if len(cmd.Args) != 3 && len(cmd.Args) != 4 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.DONE' command")
					return
				}
				var err error
				if len(cmd.Args) == 4 {
					err = s.hub.CompleteTask(string(cmd.Args[1]), string(cmd.Args[2]), string(cmd.Args[3]))
				} else {
					err = s.hub.CompleteTask(string(cmd.Args[1]), "", string(cmd.Args[2]))
				}
				if err != nil {
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

			case "TASK.LIST": // TASK.LIST [project_id]
				if len(cmd.Args) > 2 {
					conn.WriteError("ERR wrong number of arguments for 'TASK.LIST' command")
					return
				}
				var tasks []mailbox.Task
				if len(cmd.Args) == 2 {
					tasks = s.hub.ListTasks(string(cmd.Args[1]))
				} else {
					tasks = s.hub.ListTasks()
				}
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

			// --- Conversation 命令 ---

			case "CONV.GET": // CONV.GET <task_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'CONV.GET' command")
					return
				}
				entries := s.hub.GetConversation(string(cmd.Args[1]))
				b, err := json.Marshal(entries)
				if err != nil {
					conn.WriteError("ERR failed to serialize conversation")
					return
				}
				conn.WriteBulkString(string(b))

			// --- Project 命令 ---

			case "PROJ.CREATE": // PROJ.CREATE <creator_id> <name>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'PROJ.CREATE' command")
					return
				}
				projID, err := s.hub.CreateProject(string(cmd.Args[1]), string(cmd.Args[2]))
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(projID)
				}

			case "PROJ.GET": // PROJ.GET <project_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'PROJ.GET' command")
					return
				}
				proj, err := s.hub.GetProject(string(cmd.Args[1]))
				if err != nil {
					conn.WriteError("ERR " + err.Error())
					return
				}
				b, err := json.Marshal(proj)
				if err != nil {
					conn.WriteError("ERR failed to serialize project")
					return
				}
				conn.WriteBulkString(string(b))

			case "PROJ.COMPLETE": // PROJ.COMPLETE <project_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'PROJ.COMPLETE' command")
					return
				}
				if err := s.hub.CompleteProject(string(cmd.Args[1])); err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteString("OK")
				}

			case "PROJ.LIST": // PROJ.LIST
				if len(cmd.Args) != 1 {
					conn.WriteError("ERR wrong number of arguments for 'PROJ.LIST' command")
					return
				}
				projects := s.hub.ListProjects()
				b, err := json.Marshal(projects)
				if err != nil {
					conn.WriteError("ERR failed to serialize projects")
					return
				}
				conn.WriteBulkString(string(b))

			// --- Pipeline 命令 ---

			case "PIPELINE.PUBLISH": // PIPELINE.PUBLISH <creator_id> <json_steps>
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for 'PIPELINE.PUBLISH' command")
					return
				}
				var steps []mailbox.PipelineStep
				if err := json.Unmarshal(cmd.Args[2], &steps); err != nil {
					conn.WriteError("ERR invalid steps JSON: " + err.Error())
					return
				}
				pipelineID, err := s.hub.PublishPipeline(string(cmd.Args[1]), steps)
				if err != nil {
					conn.WriteError("ERR " + err.Error())
				} else {
					conn.WriteBulkString(pipelineID)
				}

			case "PIPELINE.GET": // PIPELINE.GET <pipeline_id>
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for 'PIPELINE.GET' command")
					return
				}
				pipeline, ok := s.hub.GetPipeline(string(cmd.Args[1]))
				if !ok {
					conn.WriteNull()
					return
				}
				b, err := json.Marshal(pipeline)
				if err != nil {
					conn.WriteError("ERR failed to serialize pipeline")
					return
				}
				conn.WriteBulkString(string(b))

			case "PIPELINE.LIST": // PIPELINE.LIST
				if len(cmd.Args) != 1 {
					conn.WriteError("ERR wrong number of arguments for 'PIPELINE.LIST' command")
					return
				}
				pipelines := s.hub.ListPipelines()
				b, err := json.Marshal(pipelines)
				if err != nil {
					conn.WriteError("ERR failed to serialize pipelines")
					return
				}
				conn.WriteBulkString(string(b))
			}
		},
		func(conn redcon.Conn) bool { return true },
		func(conn redcon.Conn, err error) {},
	)
}

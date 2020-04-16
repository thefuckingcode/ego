package mns

import (
	alimns "github.com/aliyun/aliyun-mns-go-sdk"
	"github.com/ebar-go/ego/component/log"
	"github.com/ebar-go/ego/component/trace"
	"github.com/ebar-go/ego/utils"
	"github.com/ebar-go/ego/utils/date"
	"github.com/ebar-go/ego/utils/json"
	"github.com/ebar-go/ego/utils/strings"
)

// Client mns客户端接口
type Client interface {
	// 生成params的sign字段
	GenerateSign(str string) string

	// 添加队列
	AddQueue(name string, handler QueueHandler, waitSecond int)

	// 获取队列
	GetQueue(name string) Queue

	// 监听队列
	ListenQueues()

	PublishMessage(topicName string, params Params, filterTag string) (*alimns.MessageSendResponse, error)
}

// Client MNS客户端
type client struct {
	// 配置
	accessKeySecret string

	// 阿里云mns实例
	instance alimns.MNSClient

	// 队列
	queueItems map[string]Queue

	// 主题
	topicItems map[string]Topic
}

// NewClient 实例化
func NewClient(url, accessKeyId, accessKeySecret string) Client {
	cli := new(client)
	cli.accessKeySecret = accessKeySecret
	cli.instance = alimns.NewAliMNSClient(url, accessKeyId, accessKeySecret)
	cli.queueItems = make(map[string]Queue)
	cli.topicItems = make(map[string]Topic)

	return cli
}

// GenerateSign 生成签名
func (cli *client) GenerateSign(str string) string {
	return strings.Md5(str + cli.accessKeySecret)
}

// AddQueue 实例化队列
func (cli *client) AddQueue(name string, handler QueueHandler, waitSecond int) {
	q := Queue{}
	q.Name = name
	q.handler = handler
	q.WaitSecond = waitSecond
	q.instance = alimns.NewMNSQueue(name, cli.instance)
	cli.queueItems[name] = q
}

// GetTopic 获取主题
func (cli *client) getTopic(name string) Topic {
	if _, ok := cli.topicItems[name]; !ok {
		cli.topicItems[name] = Topic{Name: name, Instance: alimns.NewMNSTopic(name, cli.instance)}
	}

	return cli.topicItems[name]
}

// GetQueue 获取队列
func (cli *client) GetQueue(name string) Queue {
	return cli.queueItems[name]
}

// ListenQueues 监听队列
func (cli *client) ListenQueues() {
	if len(cli.queueItems) == 0 {
		return
	}

	for _, item := range cli.queueItems {
		if item.HasHandler() {
			go cli.ReceiveMessage(item.Name)
		}
	}
}

// ReceiveMessage 接收消息并处理
func (cli *client) ReceiveMessage(queueName string) {
	q := cli.GetQueue(queueName)
	if q.WaitSecond == 0 {
		q.WaitSecond = 30
	}
	endChan := make(chan int)
	respChan := make(chan alimns.MessageReceiveResponse)
	errChan := make(chan error)
	go func() {
		select {
		case resp := <-respChan:
			{
				var params Params

				// 解析消息
				if err := json.Decode([]byte(strings.DecodeBase64(resp.MessageBody)), &params); err != nil {
					log.MQ().Error("invalidMessageBody", log.Context(map[string]interface{}{
						"err":   err.Error(),
						"trace": utils.Trace(),
					}))
				} else {

					log.MQ().Info("receiveMessage", log.Context(map[string]interface{}{
						"receiveTime": date.GetTimeStr(),
						"queue_name":  q.Name,
						"messageBody": params.Content,
						"tag":         params.Tag,
						"trace_id":    params.TraceId,
					}))

					if err := q.handler(params); err != nil {
						log.MQ().Warn("processMessageFailed", log.Context(map[string]interface{}{
							"err":   err.Error(),
							"trace": utils.Trace(),
						}))

					} else {
						// 处理成功，删除消息
						err := q.DeleteMessage(resp.ReceiptHandle)
						log.MQ().Info("deleteMessage", log.Context(map[string]interface{}{
							"receiveTime": date.GetTimeStr(),
							"queue_name":  q.Name,
							"messageBody": params.Content,
							"tag":         params.Tag,
							"trace_id":    params.TraceId,
							"err":         err,
						}))

						endChan <- 1
					}
				}

			}
		case err := <-errChan:
			{
				log.MQ().Info("receiveMessageFailed", log.Context(map[string]interface{}{
					"err":   err.Error(),
					"trace": utils.Trace(),
				}))
				endChan <- 1
			}
		}
	}()

	// 通过chan去接收数据
	q.instance.ReceiveMessage(respChan, errChan, int64(q.WaitSecond))
	<-endChan
}

// PublishMessage 发布消息
func (cli *client) PublishMessage(topicName string, params Params, filterTag string) (*alimns.MessageSendResponse, error) {
	params.TraceId = strings.Default(params.TraceId, trace.GetTraceId())
	params.Sign = strings.Default(params.Sign, cli.GenerateSign(params.TraceId))
	bytes, err := json.Encode(params)
	if err != nil {
		return nil, err
	}

	topic := cli.getTopic(topicName)
	request := alimns.MessagePublishRequest{
		MessageBody: strings.EncodeBase64([]byte(bytes)),
		MessageTag:  filterTag,
	}
	resp, err := topic.Instance.PublishMessage(request)
	if err != nil {
		return nil, err
	}

	log.MQ().Info("publishMessage", log.Context(map[string]interface{}{
		"action":          "publishMessage",
		"publish_time":    date.GetTimeStr(),
		"msectime":        date.GetMicroTimeStampStr(),
		"message_id":      resp.MessageId,
		"status_code":     resp.Code,
		"topic_name":      topic.Name,
		"message_tag":     params.Tag,
		"global_trace_id": strings.UUID(),
		"trace_id":        params.TraceId,
		"filter_tag":      filterTag,
		"sign":            params.Sign,
	}))

	return &resp, nil
}

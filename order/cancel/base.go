package cancel

import (
	"fmt"
	"log"
	"os"

	envConfig "github.com/oldfritter/goDCE/config"
	. "github.com/oldfritter/goDCE/models"
	"github.com/oldfritter/goDCE/utils"
	"github.com/streadway/amqp"
)

var (
	Assignments = make(map[int]Market)
)

func InitAssignments() {

	mainDB := utils.MainDbBegin()
	defer mainDB.DbRollback()
	var markets []Market
	mainDB.Where("order_cancel_node = ?", envConfig.CurrentEnv.Node).Find(&markets)
	for _, market := range markets {
		market.Running = Assignments[market.Id].Running
		if market.MatchingAble && !market.Running {
			Assignments[market.Id] = market
		} else if !market.MatchingAble {
			delete(Assignments, market.Id)
		}
	}
	mainDB.DbRollback()
	for id, assignment := range Assignments {
		if assignment.MatchingAble && !assignment.Running {
			go func(id int) {
				a := Assignments[id]
				subscribeMessageByQueue(&a, amqp.Table{})
			}(id)
			assignment.Running = true
			Assignments[id] = assignment
		}
	}
}

func subscribeMessageByQueue(assignment *Market, arguments amqp.Table) error {
	channel, err := utils.RabbitMqConnect.Channel()
	if err != nil {
		fmt.Errorf("Channel: %s", err)
	}
	queueName := (*assignment).OrderCancelQueue()
	queue, err := channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when usused
		false,     // exclusive
		false,     // no-wait
		arguments, // arguments
	)
	if err != nil {
		return fmt.Errorf("Queue Declare: %s", err)
	}
	channel.ExchangeDeclare((*assignment).OrderCancelExchange(), "topic", (*assignment).Durable, false, false, false, nil)
	channel.QueueBind((*assignment).OrderCancelQueue(), (*assignment).Code, (*assignment).OrderCancelExchange(), false, nil)

	msgs, err := channel.Consume(
		queue.Name, // queue
		"",         // consumer
		false,      // auto-ack
		false,      // exclusive
		false,      // no-local
		false,      // no-wait
		nil,        // args
	)
	go func(id int) {
		for d := range msgs {
			a := Assignments[id]

			logFile, err := os.Create(a.OrderCancelLogFilePath())
			defer logFile.Close()
			if err != nil {
				log.Fatalln("open log file error !")
			}
			workerLog := log.New(logFile, "[Info]", log.LstdFlags)
			workerLog.SetPrefix("[Info]")

			Cancel(&d.Body, workerLog)
			d.Ack(a.Ack)
		}
		return
	}(assignment.Id)

	return nil
}

func SubscribeReload() (err error) {
	channel, err := utils.RabbitMqConnect.Channel()
	if err != nil {
		fmt.Errorf("Channel: %s", err)
		return
	}
	channel.ExchangeDeclare(utils.AmqpGlobalConfig.Exchange.Default["key"], "topic", true, false, false, false, nil)
	channel.QueueBind(utils.AmqpGlobalConfig.Queue.Cancel["reload"], utils.AmqpGlobalConfig.Queue.Cancel["reload"], utils.AmqpGlobalConfig.Exchange.Default["key"], false, nil)
	return
}

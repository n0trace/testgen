package main

import (
	"encoding/json"
	"fmt"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/n0trace/testgen/record"
)

func main() {
	var client service.Client
	var err error
	client = rpc2.NewClient("localhost:2345")
	client.Restart(false)
	r := record.NewRecord(client, "main.UserImpl.SayHello")
	if err = r.Init(); err != nil {
		panic(err)
	}

	var cases []*record.Case
	if cases, err = r.R(); err != nil {
		panic(err)
	}
	fmt.Println(dumps2(cases, nil))
}

func dumps2(i interface{}, err error) string {
	bs, _ := json.Marshal(i)
	return string(bs)
}

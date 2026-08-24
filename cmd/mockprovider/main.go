package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/neverknowerdev/paylessforai/test/mockprovider"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:19475", "mock provider listen address")
	flag.Parse()
	server := mockprovider.New(mockprovider.Scenario{Models: []mockprovider.Model{{ID: "model-a", Name: "Model A", ContextLength: 128000, MaxCompletionTokens: 4096, PromptPrice: "0.000001", CompletionPrice: "0.000002", SupportedParameters: []string{"tools", "response_format"}}}, ResponseText: "mock response", InputTokens: 3, OutputTokens: 2, Cost: 0.000001})
	log.Printf("mock provider listening on %s", *listen)
	if err := http.ListenAndServe(*listen, server); err != nil {
		log.Fatal(err)
	}
}

package main

// #include <stdio.h>
// #include <stdlib.h>
import "C"

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"unicode"
	"unsafe"

	"github.com/ericchiang/k8s"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
)

type endpoints struct {
	IPs []string `json:"ips"`
}

// GetEndpoints searches the endpoints of a service returning a list of IP addresses.
//export GetEndpoints
func GetEndpoints(namespace, service, defSeeds *C.char) *C.char {
	ns := C.GoString(namespace)
	svc := C.GoString(service)
	seeds := C.GoString(defSeeds)

	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, seeds)

	nseeds := strings.Split(s, ",")
	client, err := k8s.NewInClusterClient()
	if err != nil {
		log.Printf("unexpected error opening a connection against API server: %v\n", err)
		log.Printf("returning default seeds: %v\n", nseeds)
		return buildEndpoints(nseeds)
	}

	ips := make([]string, 0)

	var endpoints corev1.Endpoints
	err = client.Get(context.Background(), ns, svc, &endpoints)
	if err != nil {
		log.Printf("unexpected error obtaining information about service endpoints: %v\n", err)
		log.Printf("returning default seeds: %v\n", nseeds)
		return buildEndpoints(nseeds)
	}

	for _, endpoint := range endpoints.Subsets {
		for _, address := range endpoint.Addresses {
			ips = append(ips, *address.Ip)
		}
	}

	if len(ips) == 0 {
		return buildEndpoints(nseeds)
	}

	return buildEndpoints(ips)
}

func buildEndpoints(ips []string) *C.char {
	b, err := json.Marshal(&endpoints{ips})
	if err != nil {
		log.Printf("unexpected error serializing JSON response: %v\n", err)
		rc := C.CString(`{"ips":[]}`)
		defer C.free(unsafe.Pointer(rc))
		return rc
	}

	rc := C.CString(string(b))
	defer C.free(unsafe.Pointer(rc))
	return rc
}

func main() {}

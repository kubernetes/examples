/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/xyproto/simpleredis"
)

var (
	masterPool *simpleredis.ConnectionPool
	slavePool  *simpleredis.ConnectionPool
)

type Input struct {
	InputText string `json:"input_text"`
}

type Tone struct {
	ToneName string `json:"tone_name"`
}

func ListRangeHandler(rw http.ResponseWriter, req *http.Request) {
	key := mux.Vars(req)["key"]
	list := simpleredis.NewList(slavePool, key)
	members := HandleError(list.GetAll()).([]string)
	membersJSON := HandleError(json.MarshalIndent(members, "", "  ")).([]byte)
	rw.Write(membersJSON)
}

func ListPushHandler(rw http.ResponseWriter, req *http.Request) {
	key := mux.Vars(req)["key"]
	value := mux.Vars(req)["value"]
	list := simpleredis.NewList(masterPool, key)

	// before push, let's get the tone of the value
	HandleError(nil, list.Add(value+" : "+getPrimaryTone(value)))
	ListRangeHandler(rw, req)
}

func InfoHandler(rw http.ResponseWriter, req *http.Request) {
	info := HandleError(masterPool.Get(0).Do("INFO")).([]byte)
	rw.Write(info)
}

func EnvHandler(rw http.ResponseWriter, req *http.Request) {
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		splits := strings.Split(item, "=")
		key := splits[0]
		val := strings.Join(splits[1:], "=")
		environment[key] = val
	}

	envJSON := HandleError(json.MarshalIndent(environment, "", "  ")).([]byte)
	rw.Write(envJSON)
}

func HandleError(result interface{}, err error) (r interface{}) {
	if err != nil {
		panic(err)
	}
	return result
}

func getPrimaryTone(value string) (tone string) {
	u := Input{InputText: value}
	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(u)

	res, err := http.Post("http://analyzer:80/tone", "application/json", b)
	if err != nil {
		fmt.Println("error occurred talking to the analyzer service", err)
		return ""
	}
	body := []Tone{}
	json.NewDecoder(res.Body).Decode(&body)
	if len(body) > 0 {
		// 7 tones:  anger, fear, joy, sadness, analytical, confident, and tentative
		if body[0].ToneName == "Joy" {
			return body[0].ToneName + " (✿◠‿◠)"
		} else if body[0].ToneName == "Anger" {
			return body[0].ToneName + " (ಠ_ಠ)"
		} else if body[0].ToneName == "Fear" {
			return body[0].ToneName + " (ง’̀-‘́)ง"
		} else if body[0].ToneName == "Sadness" {
			return body[0].ToneName + " （︶︿︶）"
		} else if body[0].ToneName == "Analytical" {
			return body[0].ToneName + " ( °□° )"
		} else if body[0].ToneName == "Confident" {
			return body[0].ToneName + " (▀̿Ĺ̯▀̿ ̿)"
		} else if body[0].ToneName == "Tentative" {
			return body[0].ToneName + " (•_•)"
		}
		return body[0].ToneName
	}

	return "No Tone Detected"
}

func main() {
	masterPool = simpleredis.NewConnectionPoolHost("redis-master:6379")
	defer masterPool.Close()
	slavePool = simpleredis.NewConnectionPoolHost("redis-slave:6379")
	defer slavePool.Close()

	r := mux.NewRouter()
	r.Path("/lrange/{key}").Methods("GET").HandlerFunc(ListRangeHandler)
	r.Path("/rpush/{key}/{value}").Methods("GET").HandlerFunc(ListPushHandler)
	r.Path("/info").Methods("GET").HandlerFunc(InfoHandler)
	r.Path("/env").Methods("GET").HandlerFunc(EnvHandler)

	n := negroni.Classic()
	n.UseHandler(r)
	n.Run(":3000")
}

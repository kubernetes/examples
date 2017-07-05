/*
Copyright 2017 The Kubernetes Authors.

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
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/gocql/gocql"
)

type server struct {
	cluster *gocql.ClusterConfig
	mux     *http.ServeMux
}

func newServer() (*server, error) {
	c, err := setupIfRequired()
	if err != nil {
		return nil, err
	}
	s := &server{
		cluster: c,
		mux:     http.NewServeMux(),
	}

	s.mux.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		name := r.FormValue("name")
		if name == "" {
			http.Error(w, "name must be specified", http.StatusBadRequest)
			return
		}
		if err := s.addUser(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	s.mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		names, err := s.readUsers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(names)
	})

	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return s, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) createSession() (*gocql.Session, error) {
	session, err := s.cluster.CreateSession()
	if err != nil {
		log.Fatalf("unable to create a session: %v", err)
	}
	return session, nil
}

func (s *server) addUser(name string) error {
	session, err := s.createSession()
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Query(`INSERT INTO test.users (name) VALUES (?);`, name).Exec()
}

func (s *server) readUsers() ([]string, error) {
	session, err := s.createSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	iter := session.Query(`SELECT name FROM test.users;`).Iter()
	var name string
	var names []string
	for iter.Scan(&name) {
		names = append(names, name)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func main() {
	stop := make(chan os.Signal)
	signal.Notify(stop, syscall.SIGTERM)
	s, err := newServer()
	if err != nil {
		log.Fatalf("could not create a server: %v", err)
	}

	h := &http.Server{Handler: s, Addr: ":8080"}
	go func() {
		log.Println("Serving on 0.0.0.0:8080")
		if err := h.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait for SIGTERM.
	<-stop

	time.Sleep(15 * time.Second)

	log.Println("Entering lame duck mode")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h.Shutdown(ctx)

	log.Println("Gracefully shutdown")
}

func setupIfRequired() (*gocql.ClusterConfig, error) {
	// We only need one seed here, gocql should discover the rest.
	// We add two in case the first replica is down.
	cluster := gocql.NewCluster("cassandra-0.cassandra", "cassandra-1.cassandra")
	cluster.Consistency = gocql.Two
	cluster.Timeout = 3 * time.Second
	cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: 3}
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if err := session.Query(`CREATE KEYSPACE IF NOT EXISTS test WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor' : 3};`).Exec(); err != nil {
		return nil, err
	}
	if err := session.Query(`CREATE TABLE IF NOT EXISTS test.users(name text, PRIMARY KEY(name));`).Exec(); err != nil {
		return nil, err
	}
	return cluster, nil
}

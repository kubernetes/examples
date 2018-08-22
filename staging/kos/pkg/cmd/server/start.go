/*
Copyright 2019 The Kubernetes Authors.

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

package server

import (
	"fmt"
	"io"
	"net"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	"k8s.io/examples/staging/kos/pkg/apiserver"
	networkclientset "k8s.io/examples/staging/kos/pkg/client/clientset/internalversion"
	networkinformers "k8s.io/examples/staging/kos/pkg/client/informers/internalversion"
	generatedopenapi "k8s.io/examples/staging/kos/pkg/generated/openapi"
)

const defaultEtcdPathPrefix = "/registry/network.kubernetes.io"

type NetworkAPIServerOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions
	ServerRunOptions   *genericoptions.ServerRunOptions

	StdOut io.Writer
	StdErr io.Writer
}

func NewNetworkAPIServerOptions(out, errOut io.Writer) *NetworkAPIServerOptions {
	o := &NetworkAPIServerOptions{
		RecommendedOptions: genericoptions.NewRecommendedOptions(defaultEtcdPathPrefix, apiserver.Codecs.LegacyCodec(v1alpha1.SchemeGroupVersion)),
		ServerRunOptions:   genericoptions.NewServerRunOptions(),

		StdOut: out,
		StdErr: errOut,
	}

	return o
}

// NewCommandStartNetworkAPIServer provides a CLI handler for 'start master'
// command with a default NetworkAPIServerOptions.
func NewCommandStartNetworkAPIServer(defaults *NetworkAPIServerOptions, stopCh <-chan struct{}) *cobra.Command {
	o := *defaults
	cmd := &cobra.Command{
		Short: "Launch a network API server",
		Long:  "Launch a network API server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.RunNetworkAPIServer(stopCh); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	o.RecommendedOptions.AddFlags(flags)
	o.ServerRunOptions.AddUniversalFlags(flags)

	return cmd
}

func (o NetworkAPIServerOptions) Validate(args []string) error {
	errors := []error{}
	errors = append(errors, o.ServerRunOptions.Validate()...)
	errors = append(errors, o.RecommendedOptions.Validate()...)
	return utilerrors.NewAggregate(errors)
}

func (o *NetworkAPIServerOptions) Complete() error {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	return nil
}

func (o *NetworkAPIServerOptions) Config() (*apiserver.Config, error) {
	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)
	serverConfig.EnableMetrics = true
	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = "network-apiserver"
	if err := o.RecommendedOptions.ApplyTo(serverConfig, apiserver.Scheme); err != nil {
		return nil, err
	}
	if err := o.ServerRunOptions.ApplyTo(&serverConfig.Config); err != nil {
		return nil, err
	}

	client, err := networkclientset.NewForConfig(serverConfig.LoopbackClientConfig)
	if err != nil {
		return nil, err
	}
	networkInformerFactory := networkinformers.NewSharedInformerFactory(client, 0)

	config := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig:   &apiserver.ExtraConfig{networkInformerFactory},
	}
	return config, nil
}

func (o NetworkAPIServerOptions) RunNetworkAPIServer(stopCh <-chan struct{}) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	server, err := config.Complete().New()
	if err != nil {
		return err
	}

	server.GenericAPIServer.AddPostStartHook("start-network-apiserver-informer", func(context genericapiserver.PostStartHookContext) error {
		glog.V(1).Infoln("NetworkSharedInformerFactory about to start")
		config.ExtraConfig.NetworkSharedInformerFactory.Start(context.StopCh)
		glog.V(1).Infoln("NetworkSharedInformerFactory started")
		return nil
	})
	glog.V(1).Infoln("start-network-apiserver-informers PostStartHook added")

	return server.GenericAPIServer.PrepareRun().Run(stopCh)
}

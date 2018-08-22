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

package apiserver

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"

	"k8s.io/examples/staging/kos/pkg/apis/network"
	"k8s.io/examples/staging/kos/pkg/apis/network/install"
	networkinformers "k8s.io/examples/staging/kos/pkg/client/informers/internalversion"
	networkregistry "k8s.io/examples/staging/kos/pkg/registry"
	iplockstorage "k8s.io/examples/staging/kos/pkg/registry/network/iplock"
	networkattachmentstorage "k8s.io/examples/staging/kos/pkg/registry/network/networkattachment"
	subnetstorage "k8s.io/examples/staging/kos/pkg/registry/network/subnet"
)

var (
	Scheme = runtime.NewScheme()
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	install.Install(Scheme)

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// ExtraConfig is where you should place your custom config.
type ExtraConfig struct {
	NetworkSharedInformerFactory networkinformers.SharedInformerFactory
}

type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   *ExtraConfig
}

// NetworkAPIServer contains state for a Kubernetes cluster API server which
// serves the network API group.
type NetworkAPIServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	c := completedConfig{
		cfg.GenericConfig.Complete(),
		cfg.ExtraConfig,
	}

	c.GenericConfig.Version = &version.Info{
		Major: "1",
		Minor: "0",
	}

	return CompletedConfig{&c}
}

// New returns a new instance of NetworkAPIServer from the given config.
func (c completedConfig) New() (*NetworkAPIServer, error) {
	genericServer, err := c.GenericConfig.New("network-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	s := &NetworkAPIServer{
		GenericAPIServer: genericServer,
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(network.GroupName, Scheme, metav1.ParameterCodec, Codecs)

	v1alpha1storage := map[string]rest.Storage{}
	v1alpha1storage["networkattachments"] = networkregistry.RESTInPeace(networkattachmentstorage.NewREST(Scheme, c.GenericConfig.RESTOptionsGetter))
	v1alpha1storage["subnets"] = networkregistry.RESTInPeace(subnetstorage.NewREST(Scheme, c.GenericConfig.RESTOptionsGetter, c.ExtraConfig.NetworkSharedInformerFactory))
	v1alpha1storage["iplocks"] = networkregistry.RESTInPeace(iplockstorage.NewREST(Scheme, c.GenericConfig.RESTOptionsGetter))

	apiGroupInfo.VersionedResourcesStorageMap["v1alpha1"] = v1alpha1storage

	if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, err
	}

	return s, nil
}

// Copyright 2025 The Kubernetes Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"embed"
	"fmt"

	api "k8s.io/examples/AI/modelcloud/pkg/api"

	"sigs.k8s.io/yaml"
)

//go:embed */*/*.yaml
var embedModels embed.FS

func LoadModel(modelID string) (*api.Model, error) {
	b, err := embedModels.ReadFile(modelID + "/model.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading embedded model %q: %w", modelID, err)
	}

	model := &api.Model{}
	if err := yaml.Unmarshal(b, model); err != nil {
		return nil, fmt.Errorf("parsing embedded model %q: %w", modelID, err)
	}
	return model, nil
}

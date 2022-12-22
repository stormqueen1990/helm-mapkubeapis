/*
Copyright

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

package v3

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"reflect"

	"github.com/pkg/errors"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"

	"github.com/helm/helm-mapkubeapis/pkg/common"
	"gopkg.in/yaml.v3"
)

// MapReleaseWithUnSupportedAPIs checks the latest release version for any deprecated or removed APIs in its metadata
// If it finds any, it will create a new release version with the APIs mapped to the supported versions
func MapReleaseWithUnSupportedAPIs(mapOptions common.MapOptions) error {
	cfg, err := GetActionConfig(mapOptions.ReleaseNamespace, mapOptions.KubeConfig)
	if err != nil {
		return errors.Wrap(err, "failed to get Helm action configuration")
	}

	var releaseName = mapOptions.ReleaseName
	log.Printf("Get release '%s' latest version.\n", releaseName)
	releaseToMap, err := getLatestRelease(releaseName, cfg)
	if err != nil {
		return errors.Wrapf(err, "failed to get release '%s' latest version", mapOptions.ReleaseName)
	}

	log.Printf("Check release '%s' for deprecated or removed APIs...\n", releaseName)
	origManifest, err := decodeManifests(releaseToMap.Manifest)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal manifests")
	}

	modifiedManifest, err := common.ReplaceManifestUnSupportedAPIs(origManifest, mapOptions.MapFile, mapOptions.KubeConfig)
	if err != nil {
		return err
	}
	log.Printf("Finished checking release '%s' for deprecated or removed APIs.\n", releaseName)
	if reflect.DeepEqual(modifiedManifest, origManifest) {
		log.Printf("Release '%s' has no deprecated or removed APIs.\n", releaseName)
		return nil
	}

	if mapOptions.DryRun {
		log.Printf("Deprecated or removed APIs exist, for release: %s.\n", releaseName)
	} else {
		log.Printf("Deprecated or removed APIs exist, updating release: %s.\n", releaseName)

		newManifest, err := encodeManifests(modifiedManifest)
		if err != nil {
			return errors.Wrapf(err, "failed to encode manifests")
		}

		if err := updateRelease(releaseToMap, newManifest, cfg); err != nil {
			return errors.Wrapf(err, "failed to update release '%s'", releaseName)
		}
		log.Printf("Release '%s' with deprecated or removed APIs updated successfully to new version.\n", releaseName)
	}

	return nil
}

func updateRelease(origRelease *release.Release, modifiedManifest string, cfg *action.Configuration) error {
	// Update current release version to be superseded
	log.Printf("Set status of release version '%s' to 'superseded'.\n", getReleaseVersionName(origRelease))
	origRelease.Info.Status = release.StatusSuperseded
	if err := cfg.Releases.Update(origRelease); err != nil {
		return errors.Wrapf(err, "failed to update release version '%s': %s", getReleaseVersionName(origRelease))
	}
	log.Printf("Release version '%s' updated successfully.\n", getReleaseVersionName(origRelease))

	// Using a shallow copy of current release version to update the object with the modification
	// and then store this new version
	var newRelease = origRelease
	newRelease.Manifest = modifiedManifest
	newRelease.Info.Description = common.UpgradeDescription
	newRelease.Info.LastDeployed = cfg.Now()
	newRelease.Version = origRelease.Version + 1
	newRelease.Info.Status = release.StatusDeployed
	log.Printf("Add release version '%s' with updated supported APIs.\n", getReleaseVersionName(origRelease))
	if err := cfg.Releases.Create(newRelease); err != nil {
		return errors.Wrapf(err, "failed to create new release version '%s': %s", getReleaseVersionName(origRelease))
	}
	log.Printf("Release version '%s' added successfully.\n", getReleaseVersionName(origRelease))
	return nil
}

func getLatestRelease(releaseName string, cfg *action.Configuration) (*release.Release, error) {
	return cfg.Releases.Last(releaseName)
}

func getReleaseVersionName(rel *release.Release) string {
	return fmt.Sprintf("%s.v%d", rel.Name, rel.Version)
}

// decodeManifests decodes the release secret into a list of manifests that can be edited
func decodeManifests(releaseManifestData string) ([]map[string]interface{}, error) {
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(releaseManifestData)))

	manifests := make([]map[string]interface{}, 0)
	for {
		var value map[string]interface{}
		err := decoder.Decode(&value)

		// we reached the end of the stream
		if errors.Is(err, io.EOF) {
			break
		}

		// object is empty, no need to inspect it
		if value == nil || len(value) == 0 {
			continue
		}

		// another non-recoverable error happened, break processing here
		if err != nil {
			return nil, err
		}

		manifests = append(manifests, value)
	}

	return manifests, nil
}

// encodeManifests creates a new YAML representation of the edited manifests
func encodeManifests(manifests []map[string]interface{}) (string, error) {
	marshalledYaml := bytes.NewBuffer(make([]byte, 0, 1024*len(manifests))) // create a buffer with 1 KB capacity per manifest
	encoder := yaml.NewEncoder(marshalledYaml)
	encoder.SetIndent(2) // TODO make this parameterizable

	for _, manifest := range manifests {
		if err := encoder.Encode(manifest); err != nil {
			return "", err
		}
	}

	if err := encoder.Close(); err != nil {
		return "", err
	}

	// the go-yaml encoder does not add the "---" header, but we need it for Helm
	newYamlContent := fmt.Sprintf("---\n%s", marshalledYaml.Bytes())

	return newYamlContent, nil
}

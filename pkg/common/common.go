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

package common

import (
	"fmt"
	"log"

	utils "github.com/maorfr/helm-plugin-utils/pkg"
	"github.com/pkg/errors"
	"golang.org/x/mod/semver"

	"github.com/helm/helm-mapkubeapis/pkg/mapping"
)

// KubeConfig are the Kubernetes configuration settings
type KubeConfig struct {
	Context string
	File    string
}

// MapOptions are the options for mapping deprecated APIs in a release
type MapOptions struct {
	DryRun           bool
	KubeConfig       KubeConfig
	MapFile          string
	ReleaseName      string
	ReleaseNamespace string
}

const (
	// UpgradeDescription is description of why release was upgraded
	UpgradeDescription = "Kubernetes deprecated API upgrade - DO NOT rollback from this version"

	// ApiVersionFieldName is the name of the field in the manifest that holds the API version and group information
	ApiVersionFieldName = "apiVersion"

	// KindFieldName is the name of the field in the manifest that holds the Kind information
	KindFieldName = "kind"
)

// ReplaceManifestUnSupportedAPIs returns a release manifest with deprecated or removed
// Kubernetes APIs updated to supported APIs
func ReplaceManifestUnSupportedAPIs(origManifest []map[string]interface{}, mapFile string, kubeConfig KubeConfig) ([]map[string]interface{}, error) {
	var modifiedManifest = origManifest
	var err error
	var mapMetadata *mapping.Metadata

	// Load the mapping data
	if mapMetadata, err = mapping.LoadMapfile(mapFile); err != nil {
		return nil, errors.Wrapf(err, "Failed to load mapping file: %s", mapFile)
	}

	// get the Kubernetes server version
	kubeVersionStr, err := getKubernetesServerVersion(kubeConfig)
	if err != nil {
		return nil, err
	}
	if !semver.IsValid(kubeVersionStr) {
		return nil, errors.Errorf("Failed to get Kubernetes server version")
	}

	// Check for deprecated or removed APIs and map accordingly to supported versions
	for _, mapping := range mapMetadata.Mappings {
		deprecatedAPI := mapping.DeprecatedAPI
		supportedAPI := mapping.NewAPI
		var apiVersionStr string
		if mapping.DeprecatedInVersion != "" {
			apiVersionStr = mapping.DeprecatedInVersion
		} else {
			apiVersionStr = mapping.RemovedInVersion
		}
		if !semver.IsValid(apiVersionStr) {
			return nil, errors.Errorf("Failed to get the deprecated or removed Kubernetes version for API: %s", deprecatedAPI)
		}

		if semver.Compare(apiVersionStr, kubeVersionStr) > 0 {
			log.Printf("The following API does not require mapping as the "+
				"API is not deprecated or removed in Kubernetes '%s':\n\"%s\"\n", apiVersionStr,
				deprecatedAPI)
			continue
		}

		apiVersion := fmt.Sprintf("%v/%v", deprecatedAPI.Group, deprecatedAPI.Version)

		count := 0
		var logFormat string
		// If no superseding supported API is found, this means we should remove the manifest entirely
		if supportedAPI.Kind == "" || supportedAPI.Group == "" {
			logFormat = fmt.Sprintf("Found %%d instances of the removed Kubernetes API:\n\"%s\"\n", deprecatedAPI)

			for index, manifest := range modifiedManifest {
				if manifest[ApiVersionFieldName] == apiVersion && manifest[KindFieldName] == deprecatedAPI.Kind {
					// Remove the current manifest from the release as it does not have a superseding API.
					modifiedManifest = append(modifiedManifest[:index], modifiedManifest[index+1:]...)
				}
			}
		} else {
			logFormat = fmt.Sprintf("Found %%d instances of deprecated or removed Kubernetes API:\n\"%s\"\nSupported API equivalent:\n\"%s\"\n", deprecatedAPI, supportedAPI)

			for _, manifest := range modifiedManifest {
				apiVersion := fmt.Sprintf("%v/%v", deprecatedAPI.Group, deprecatedAPI.Version)

				if manifest[ApiVersionFieldName] == apiVersion && manifest[KindFieldName] == deprecatedAPI.Kind {
					newApiVersion := fmt.Sprintf("%v/%v", supportedAPI.Group, supportedAPI.Version)
					manifest[ApiVersionFieldName] = newApiVersion
					count++
				}
			}
		}

		// output the number of occurrences found + the kind of occurrence (removal or version upgrade)
		if count > 0 {
			log.Printf(logFormat, count)
		}
	}

	return modifiedManifest, nil
}

func getKubernetesServerVersion(kubeConfig KubeConfig) (string, error) {
	clientSet := utils.GetClientSetWithKubeConfig(kubeConfig.File, kubeConfig.Context)
	if clientSet == nil {
		return "", errors.Errorf("kubernetes cluster unreachable")
	}
	kubeVersion, err := clientSet.ServerVersion()
	if err != nil {
		return "", errors.Wrap(err, "kubernetes cluster unreachable")
	}
	return kubeVersion.GitVersion, nil
}

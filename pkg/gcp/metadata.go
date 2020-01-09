package gcp

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

func GetProject() (string, error) {
	return getMetadata("project/project-id")
}

func GetClusterName() (string, error) {
	return getMetadata("/instance/attributes/cluster-name")
}

func GetClusterLocation() (string, error) {
	return getMetadata("/instance/attributes/cluster-location")
}

func metadataRequest(urlPath string) (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET",
		fmt.Sprintf("http://metadata/computeMetadata/v1/%s", urlPath), nil)
	req.Header.Add("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(bodyBytes), nil
}

func getMetadata(urlPath string) (string, error) {
	for i := 1; i <= 3; i++ {
		p, err := metadataRequest(urlPath)
		if p != "" {
			return p, err
		}
		time.Sleep(time.Second * time.Duration(i))
	}
	return "", fmt.Errorf("Failed to resolve metadata from %s", urlPath)
}

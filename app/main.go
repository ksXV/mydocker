package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type DockerManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Size      int    `json:"size"`
		Digest    string `json:"digest"`
	} `json:"config"`

	Layers []struct {
		MediaType string `json:"mediaType"`
		Size      int    `json:"size"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

const (
	authUrl     = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/%s:pull"
	manifestUrl = "https://registry.hub.docker.com/v2/library/%s/manifests/%s"
	layerUrl    = "https://registry.hub.docker.com/v2/library/%s/blobs/%s"
)

// copy a file from source path to destination path while preserving the file permissions
func copyFile(src string, dest string) error {
	//open the source file for reading
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	//Ensure directory for destination file exists,creating it with property permissions
	err = os.MkdirAll(filepath.Dir(dest), 0755)
	if err != nil {
		return err
	}

	//Create the destination file for writing
	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	//copy the content from src file to dest file
	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return err
	}

	//Retrieve the file mode of source file
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	err = destFile.Chmod(srcInfo.Mode())
	if err != nil {
		return err

	}

	return nil
}

func createFile(number int, contents io.ReadCloser) (*os.File, error) {
	file, err := os.Create(fmt.Sprintf("layer-%d", number))
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(file, contents)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func extractTar(filename, dest string) {
	cmd := exec.Command("tar", "xzf", filename, "-C", dest)
	err := cmd.Run()
	if err != nil {
		log.Fatalln(err.Error())
	}
	// Remove the tar file after extraction
	err = os.Remove(filename)
	if err != nil {
		log.Fatalln(err.Error())
	}
}

func handleLayers(manifest DockerManifest, imageData []string, token interface{}, tempDir string) {
	client := &http.Client{}
	for n, layer := range manifest.Layers {
		downloadUrl := fmt.Sprintf(layerUrl, imageData[0], layer.Digest)
		req, err := http.NewRequest("GET", downloadUrl, nil)
		if err != nil {
			log.Fatalf(err.Error())
		}
		req.Header.Add("Authorization", "Bearer "+token.(string))
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")

		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf(err.Error())
		}
		tarFile, err := createFile(n, resp.Body)
		if err != nil {
			log.Fatalf(err.Error())
		}
		extractTar(tarFile.Name(), tempDir)
	}
}

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	if len(os.Args) < 4 {
		log.Fatalf("Usage: your_docker.sh run <image> <command> <arg1> <arg2> ... \n")
	}
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]
	image := os.Args[2]
	imageData := make([]string, 2)
	if strings.Contains(image, ":") {
		imageData[1] = strings.Split(image, ":")[1]
	} else {
		imageData[1] = "latest"
	}
	imageData[0] = strings.Split(image, ":")[0]

	resp, err := http.Get(fmt.Sprintf(authUrl, imageData[0]))

	// i should type a struct for this but i am too lazy 
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	token := result["token"]

	client := &http.Client{}
	url := fmt.Sprintf(manifestUrl, imageData[0], imageData[1])

	req, err := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "Bearer "+token.(string))
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	resp, err = client.Do(req)
	body, err := io.ReadAll(resp.Body)
	var manifest DockerManifest
	json.Unmarshal(body, &manifest)

	tempDir, err := os.MkdirTemp("", "sandbox")
	if err != nil {
		log.Fatalln(":(", err.Error())
	}
    handleLayers(manifest, imageData, token, tempDir)

	err = syscall.Chroot(tempDir)
	if err != nil {
		log.Fatalln(":(", err.Error())
	}

	err = os.Chdir("/")
	if err != nil {
		log.Fatalln(":(", err.Error())
	}

	err = os.Mkdir("/dev", 0755)

	devNull, err := os.Create("/dev/null")
	if err != nil {
		log.Fatalln(":(", err.Error())
	}
	devNull.Close()

	cmd := exec.Command(command, args...)

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID,
	}

	err = cmd.Run()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			log.Println("Exited")
			returnCode := exitError.ExitCode()
			os.Exit(returnCode)
		}
		log.Println("Exited", err.Error())

		os.Exit(1)
	}
}

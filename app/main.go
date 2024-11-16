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

func copyFile(src string, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	err = os.MkdirAll(filepath.Dir(dest), 0755)
	if err != nil {
		return err
	}

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return err
	}

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

func extractTar(filename, dest string) error {
	cmd := exec.Command("tar", "xzf", filename, "-C", dest)
	err := cmd.Run()
	if err != nil {
        return err
	}
	err = os.Remove(filename)
	if err != nil {
        return err
	}
    return nil
}

func handleLayers(manifest *DockerManifest, imageData []string, token string, tempDir string) error {
	client := &http.Client{}
	for n, layer := range manifest.Layers {
		downloadUrl := fmt.Sprintf(layerUrl, imageData[0], layer.Digest)
		req, err := http.NewRequest("GET", downloadUrl, nil)
		if err != nil {
            return err
		}
		req.Header.Add("Authorization", "Bearer "+token)
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")

		resp, err := client.Do(req)
		if err != nil {
            return err
		}
		tarFile, err := createFile(n, resp.Body)
		if err != nil {
            return err
		}
		err = extractTar(tarFile.Name(), tempDir)
        if err != nil {
            return err
        }
	}
    return nil
}

func getToken(repo string) (string, error) {
	resp, err := http.Get(fmt.Sprintf(authUrl, repo))
    if err != nil {
        return "", err
    }

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
    if err != nil {
        return "", err
    }
	token := result["token"].(string)

    return token, nil
}

func getDockerManifest(repo, version, token string) (*DockerManifest, error) {
    client := &http.Client{}
    url := fmt.Sprintf(manifestUrl, repo, version)

	req, err := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")

    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }

	body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err 
    }

	var manifest DockerManifest
	err = json.Unmarshal(body, &manifest)
    if err != nil {
        return nil, err 
    }
    
    log.Println(manifest)

    return &manifest, nil
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
    token, err := getToken(imageData[0])
    if err != nil {
        log.Fatalln(err.Error())
    }


	tempDir, err := os.MkdirTemp("", "sandbox")
	if err != nil {
		log.Fatalln(err.Error())
	}

    manifest, err := getDockerManifest(imageData[0], imageData[1], token)
    if err != nil {
        log.Fatalln(err.Error())
    }
    log.Println(manifest)
    err = handleLayers(manifest, imageData, token, tempDir)
    if err != nil {
        log.Fatalln(err.Error())
    }

	err = syscall.Chroot(tempDir)
	if err != nil {
		log.Fatalln(err.Error())
	}

	err = os.Chdir("/")
	if err != nil {
		log.Fatalln(err.Error())
	}

	err = os.Mkdir("/dev", 0755)
    if err != nil {
        log.Fatalln(err.Error())
    }

	devNull, err := os.Create("/dev/null")
	if err != nil {
		log.Fatalln(err.Error())
	}
	err = devNull.Close()
    if err != nil {
        log.Fatalln(err.Error())
    }

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
		log.Fatalln("Exited", err.Error())
	}
}

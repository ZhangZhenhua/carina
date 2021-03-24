package deviceplugin

import (
	"bocloud.com/cloudnative/carina/pkg/devicemanager/volume"
	"bocloud.com/cloudnative/carina/pkg/deviceplugin/v1beta1"
	"bocloud.com/cloudnative/carina/utils"
	"bocloud.com/cloudnative/carina/utils/log"
	"github.com/fsnotify/fsnotify"
	"os"
	// 依赖冲突，把整个proto目录挪移过来
	//pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func Run(volumeManager volume.LocalVolume, stopChan <-chan struct{}) {

	watcher, err := newFSWatcher(v1beta1.DevicePluginPath)
	if err != nil {
		log.Errorf("Failed to create FS watcher: %v", err)
		os.Exit(-1)
	}
	defer watcher.Close()

	plugins := []*CarinaDevicePlugin{}
restart:
	for _, p := range plugins {
		_ = p.Stop()
	}

	log.Info("Retreiving plugins.")
	for _, d := range []string{utils.DeviceVGSSD, utils.DeviceVGHDD} {
		c := make(chan struct{}, 5)
		plugins = append(plugins, NewCarinaDevicePlugin(
			utils.DeviceCapacityKeyPrefix+d,
			volumeManager,
			c,
			v1beta1.DevicePluginPath+d+".sock",
		))
		// 注册通知服务
		log.Info("register volume notice server.")
		volumeManager.RegisterNoticeServer(d, c)
	}

	started := 0
	pluginStartError := make(chan struct{})
	for _, p := range plugins {
		if err := p.Start(); err != nil {
			log.Error("Could not contact Kubelet, retrying. Did you enable the device plugin feature gate?")
			close(pluginStartError)
			goto events
		}
		started++
	}
	if started == 0 {
		log.Info("No devices found, Waiting indefinitely.")
	}

events:
	// Start an infinite loop, waiting for several indicators to either log
	// some messages, trigger a restart of the plugins, or exit the program.
	for {
		select {
		// If there was an error starting any plugins, restart them all.
		case <-pluginStartError:
			goto restart

		// Detect a kubelet restart by watching for a newly created
		// 'pluginapi.KubeletSocket' file. When this occurs, restart this loop,
		// restarting all of the plugins in the process.
		case event := <-watcher.Events:
			if event.Name == v1beta1.KubeletSocket && event.Op&fsnotify.Create == fsnotify.Create {
				log.Infof("inotify: %s created, restarting.", v1beta1.KubeletSocket)
				goto restart
			}

		// Watch for any other fs errors and log them.
		case err := <-watcher.Errors:
			log.Infof("inotify: %s", err)

		case <-stopChan:
			for _, p := range plugins {
				_ = p.stop
			}
			return
		}
	}
}
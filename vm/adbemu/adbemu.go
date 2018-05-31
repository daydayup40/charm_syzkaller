// Copyright 2015 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package qemu

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/syzkaller/pkg/config"
	. "github.com/google/syzkaller/pkg/log"
	"github.com/google/syzkaller/pkg/osutil"
	"github.com/google/syzkaller/vm/vmimpl"
)

const (
	hostAddr = "10.0.2.10"
)

func init() {
	vmimpl.Register("adbemu", ctor)
}

type Config struct {
	Count     int      // number of VMs to use
	SdkPath   string   // Optional path to the Android SDK or obtained from environment variable ANDROID_SDK
	AdbBin    string   // Optional adb path. Must be set if sdk path could not be determined
	Emu       string   // Optional emulator binary path. Must be set if sdk path could not be determined
	Avd       []string // Android virtual device name for emulator -avd argument (without "@")
	Kernel    string   // Path to kernel image e.g. arch/x86/boot/bzImage
	Avd_Args  []string // Additional Android emulator arguments (before -qemu after -avd $Avd)
	Qemu_Args string   // Additional Android emulator arguments (after -qemu)
	//Charm start
	Emulator      []string //Passing android emulator port here
	Device_id     []string //Device ID for specific emulator
	Dev_file_name []string //It is to keep usb dev file name of previous instance
	Start_time    []int
	//Charm end
}

type Pool struct {
	env *vmimpl.Env
	cfg *Config
	//Charm start
	inst *instance
	//Charm end
}

type instance struct {
	cfg     *Config
	port    int
	workdir string
	rpipe   io.ReadCloser
	wpipe   io.WriteCloser
	qemu    *exec.Cmd
	waiterC chan error
	merger  *vmimpl.OutputMerger
	debug   bool
	// Charm start
	Device_id    string /* Phone ID */
	Avd          string
	Avd_Args     string
	device       string /* Emulator ID */
	terminate    chan bool
	terminated   bool
	usb_dev_file string
	Index        int
	//Charm end
}

func ctor(env *vmimpl.Env) (vmimpl.Pool, error) {
	cfg := &Config{
		Count:   1,
		SdkPath: "",
		AdbBin:  "",
		Emu:     "",
		//Avd:       "",
		Kernel: "",
		//Avd_Args:  "-verbose -wipe-data -show-kernel -no-window",
		Qemu_Args: "-enable-kvm",
	}

	if err := config.LoadData(env.Config, cfg); err != nil {
		return nil, err
	}
	if cfg.Count < 1 || cfg.Count > 1000 {
		return nil, fmt.Errorf("invalid config param count: %v, want [1, 1000]", cfg.Count)
	}
	if env.Debug {
		Logf(0, "env.Debug is set")
		cfg.Count = 1
	}

	if env.Image == "9p" {
		return nil, fmt.Errorf("9p image is not unsupported")
	}
	if cfg.Kernel != "" {
		if _, err := os.Stat(cfg.Kernel); err != nil {
			return nil, fmt.Errorf("kernel image file '%v' does not exist: %v", cfg.Kernel, err)
		}
	}
	if cfg.SdkPath == "" {
		cfg.SdkPath = os.Getenv("ANDROID_SDK")
	}
	if cfg.SdkPath == "" {
		return nil, fmt.Errorf("ANDROID_SDK must be set")
	}
	if cfg.AdbBin == "" {
		//Charm start
		////		cfg.AdbBin = cfg.SdkPath + "/platform-tools/adb"
		cfg.AdbBin = "/usr/bin/adb"
		//Charm end
	}
	if cfg.Emu == "" {
		cfg.Emu = cfg.SdkPath + "/tools/emulator"
	}

	for i := 0; i < cfg.Count; i++ {
		if !containsAvd(cfg.Emu, cfg.Avd[i]) {
			return nil, fmt.Errorf("avd '%v' doest not exist", cfg.Avd[i])
		}
	}

	pool := &Pool{
		cfg: cfg,
		env: env,
	}
	return pool, nil
}

func (pool *Pool) Count() int {
	return pool.cfg.Count
}

//Charm start
//func (inst *instance) Shutdown(error) {
func (pool *Pool) Shutdown() {
	Logf(0, ".........................shutting down..........................")
	exec.Command("adb", "-s", "emulator-5554", "emu", "kill").Start()
}

//Charm end

func (pool *Pool) Create(workdir string, index int) (vmimpl.Instance, error) {
	inst := &instance{
		cfg: pool.cfg,
		//		closed:  make(chan bool),
		workdir:   workdir,
		debug:     pool.env.Debug,
		device:    pool.cfg.Emulator[index],
		Device_id: pool.cfg.Device_id[index],
		Avd:       pool.cfg.Avd[index],
		Avd_Args:  pool.cfg.Avd_Args[index],
		//Charm start
		Index: index,
		//Charm end
	}

	closeInst := inst
	defer func() {
		if closeInst != nil {
			closeInst.Close()
		}
	}()
	//Charm start
	////if err := inst.repair(); err != nil {
	////	return nil, err
	////}
	//Charm end
	var err error
	inst.rpipe, inst.wpipe, err = osutil.LongPipe()
	if err != nil {
		return nil, err
	}

	if err := inst.Boot(); err != nil {
		return nil, err
	}

	closeInst = nil
	return inst, nil
}

func containsAvd(emulatorPath string, avd string) bool {
	cmd := exec.Command(emulatorPath, "-list-avds")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	s := bufio.NewReader(stdout)
	for {
		line, err := s.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.Compare(line, avd) == 0 {
			return true
		}
	}
	return false
}

func (inst *instance) Close() {
	Logf(0, ".........................Close.........................")
	inst.close(true)
}

func (inst *instance) close(removeWorkDir bool) {
	if inst.qemu != nil {
		//Charm start
		////inst.qemu.Process.Kill()
		if !inst.terminated {
			inst.terminate <- true
		}
		//Charm end
		err := <-inst.waiterC
		inst.waiterC <- err // repost it for waiting goroutines
	}
	if inst.merger != nil {
		inst.merger.Wait()
	}
	if inst.rpipe != nil {
		inst.rpipe.Close()
	}
	if inst.wpipe != nil {
		inst.wpipe.Close()
	}
	os.Remove(filepath.Join(inst.workdir, "key"))
	if removeWorkDir {
		os.RemoveAll(inst.workdir)
	}
}

func (inst *instance) Boot() error {
	//Charm start
	crash_happened := false
	prev_was_short := false
	time.Sleep(10 * time.Second)
	dev_file_bytes, err2 := exec.Command("/usr/bin/python", "/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py", inst.Device_id).Output()
	if err2 != nil {
		return fmt.Errorf("/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py failed: %v", err2)
	}
	dev_file2 := fmt.Sprintf("%s", dev_file_bytes)
	Logf(0, "dev_file_name is %s, comparing it with previous dev_file_name %s", dev_file2, inst.cfg.Dev_file_name[inst.Index])

	if strings.Compare("0", inst.cfg.Dev_file_name[inst.Index]) != 0 {
		if strings.Compare(dev_file2, inst.cfg.Dev_file_name[inst.Index]) != 0 {
			crash_happened = true
			Logf(0, "phone crashed last time. crash happened =true\n")
		} else {
			Logf(0, "phone did not crash last time. crash happend=false\n")
		}
	} else {

		Logf(0, "this is the first boot. crash happened=false\n")
	}
	time_now := time.Now().Second() + time.Now().Minute()*60 + time.Now().Hour()*3600
	last_run_duration := time_now - inst.cfg.Start_time[inst.Index]
	if last_run_duration < 2*60 {
		Logf(0, "Previous run was too short. prev_was_short=true\n")
		prev_was_short = true
	}

	if crash_happened == true || prev_was_short == true {
		//reboot the phone and wait for three minuets in this case
		if _, err := inst.Phoneadb("reboot"); err != nil {
			/* Now we'll try to reboot with the Charm USB channel */
			_, err2 = exec.Command("/home/charm/Javad/reboot_charm/CharmPhoneReboot.o", dev_file2).Output()
			if err2 != nil {
				return fmt.Errorf("/home/charm/Javad/reboot_charm/CharmPhoneReboot.o failed: %v", err2)
			}
		}
		if _, err := inst.Phoneadb("wait-for-device"); err != nil {
			return fmt.Errorf("adb wait-for-device failed: %v", err)
		}
		Logf(0, "wait for 120 seconds to make sure usb  is stable\n")
		time.Sleep(120 * time.Second)
	}
	if _, err := inst.Phoneadb("reboot"); err != nil {
		dev_file_bytes4, err4 := exec.Command("/usr/bin/python", "/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py", inst.Device_id).Output()
		if err4 != nil {
			return fmt.Errorf("/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py failed: %v", err4)
		}
		dev_file4 := fmt.Sprintf("%s", dev_file_bytes4)
		_, err4 = exec.Command("/home/charm/Javad/reboot_charm/CharmPhoneReboot.o", dev_file4).Output()
		if err4 != nil {
			return fmt.Errorf("/home/charm/Javad/reboot_charm/CharmPhoneReboot.o failed: %v", err4)
		}
	}

	if _, err := inst.Phoneadb("wait-for-device"); err != nil {
		return fmt.Errorf("adb wait-for-device failed: %v", err)
	}

	time.Sleep(60 * time.Second)
	dev_file, err := exec.Command("/usr/bin/python", "/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py", inst.Device_id).Output()
	if err != nil {
		return fmt.Errorf("/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py failed: %v", err)
	}
	inst.usb_dev_file = fmt.Sprintf("%s", dev_file)
	inst.cfg.Dev_file_name[inst.Index] = inst.usb_dev_file

	for {
		// Find an unused TCP port.
		inst.port = rand.Intn(64<<10-1<<10) + 1<<10
		ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%v", inst.port))
		if err == nil {
			ln.Close()
			break
		}
	}
	inst.cfg.Start_time[inst.Index] = time.Now().Second() + time.Now().Minute()*60 + time.Now().Hour()*3600

	args := []string{"-avd", inst.Avd}
	if inst.cfg.Kernel != "" {
		args = append(args, "-kernel", inst.cfg.Kernel)
	}

	// Avd_Args are the arguments before -qemu
	// For default value,  see func ctor(env *vmimpl.Env) (vmimpl.Pool, error)
	args = append(args, strings.Split(inst.Avd_Args, " ")...)
	args = append(args, "-charm-usb-dev-file")
	args = append(args, fmt.Sprintf("%s", inst.usb_dev_file))
	if inst.debug {
		Logf(0, "running command: %v %#v", inst.cfg.Emu, args)
	}
	qemu := exec.Command(inst.cfg.Emu, args...)
	qemu.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	qemu.Stdout = inst.wpipe
	qemu.Stderr = inst.wpipe
	if err := qemu.Start(); err != nil {
		return fmt.Errorf("failed to start %v %+v: %v", inst.cfg.Emu, args, err)
	}
	inst.wpipe.Close()
	inst.wpipe = nil
	inst.qemu = qemu
	var tee io.Writer
	if inst.debug {
		tee = os.Stdout
	}
	inst.merger = vmimpl.NewOutputMerger(tee)
	inst.merger.Add("qemu", inst.rpipe)
	inst.rpipe = nil

	var bootOutput []byte
	bootOutputStop := make(chan bool)
	go func() {
		for {
			select {
			case out := <-inst.merger.Output:
				bootOutput = append(bootOutput, out...)
			case <-bootOutputStop:
				close(bootOutputStop)
				return
			}
		}
	}()

	// Wait for the qemu asynchronously.
	inst.waiterC = make(chan error, 1)
	go func() {
		err := qemu.Wait()
		inst.waiterC <- err
	}()

	// Wait for device serial number to appear.
	if inst.debug {
		Logf(0, "Looking for Android device sn")
	}
	start := time.Now()
	for {
		out := string(bootOutput[:])
		break
		if index := strings.Index(out, "emulator: Serial number of this emulator (for ADB):"); index >= 0 {
			if cnt, _ := fmt.Sscanf(out[index:], "emulator: Serial number of this emulator (for ADB): %s\n", &inst.device); cnt == 1 {
				break // Found device serial number
			}
		}

		select {
		case err := <-inst.waiterC:
			inst.waiterC <- err     // repost it for Close
			time.Sleep(time.Second) // wait for any pending output
			bootOutputStop <- true
			<-bootOutputStop
			return fmt.Errorf("qemu stopped:\n%v\n", string(bootOutput))
		default:
		}
		if time.Since(start) > 10*time.Minute {
			bootOutputStop <- true
			<-bootOutputStop
			return fmt.Errorf("serial number not found: \n%v\n", string(bootOutput))
		}
	}
	bootOutputStop <- true

	/*
	 * Reboot the phone if emulator is not booting after a time threshold.
	 * This is a sign that Charm USB channel is malfunctioning (hence reboot with ADB)
	 */
	done := make(chan bool)
	go func() {
		select {
		case <-time.After(3 * time.Minute):
			if _, err := inst.Phoneadb("reboot"); err != nil {
				Logf(0, "Boot[17.1]: Now there's nothing we can do.")
				/* We can't do anything in this case. */
			}
		case <-done:
		}
	}()
	if _, err := inst.adb("wait-for-device"); err != nil {
		return fmt.Errorf("wait-for-device: %v", err)
	}
	close(done)
	if _, err := inst.adb("root"); err != nil {
		return fmt.Errorf("adb root failed: %v", err)
	}
	time.Sleep(10 * time.Second)

	if _, err := inst.adb("remount"); err != nil {
		return fmt.Errorf("Remount failed: %v", err)
	}
	return nil
}

/* Forward port via adb reverse */
func (inst *instance) Forward(port int) (string, error) {
	// If 35099 turns out to be busy, try to forward random ports several times.
	devicePort := port // 35099
	Logf(0, "Port is:%v", port)
	/*_, err := inst.adb("reverse", fmt.Sprintf("tcp:%v", devicePort), fmt.Sprintf("tcp:%v", port))
	if err != nil {
		return "", err
	}*/
	//return fmt.Sprintf("127.0.0.1:%v", devicePort), nil
	return fmt.Sprintf("10.0.2.2:%v", devicePort), nil
}

func (inst *instance) adb_orig(args ...string) ([]byte, error) {
	args = append([]string{"-s", inst.device}, args...)
	if inst.debug {
		Logf(0, "running command: adb %+v", args)
	}
	rpipe, wpipe, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe: %v", err)
	}
	defer wpipe.Close()
	defer rpipe.Close()
	cmd := exec.Command(inst.cfg.AdbBin, args...)
	cmd.Stdout = wpipe
	cmd.Stderr = wpipe
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wpipe.Close()
	done := make(chan bool)
	go func() {
		select {
		case <-time.After(120 * time.Second):
			Logf(0, "adb hanged (Oops)")
			time.Sleep(300 * time.Second)
			cmd.Process.Kill()
		case <-done:
		}
	}()
	if err := cmd.Wait(); err != nil {
		close(done)
		out, _ := ioutil.ReadAll(rpipe)
		if inst.debug {
			Logf(0, "adb failed: %v\n%s", err, out)
		}
		//Charm start
		////os.Exit(1)
		//Charm end
		return nil, fmt.Errorf("adb %+v failed: %v\n%s", args, err, out)
	}
	close(done)
	if inst.debug {
		Logf(0, "adb returned")
	}
	out, _ := ioutil.ReadAll(rpipe)
	return out, nil
}

//Charm  start
func (inst *instance) adb(args ...string) ([]byte, error) {
	var out []byte
	var err error
	for i := 0; i < 10; i++ {
		out, err = inst.adb_orig(args...)
		if err != nil {
			time.Sleep(10 * time.Second)
			continue
		}

		break
	}

	if err != nil {
		Logf(0, "ERROR: Could not adb to the emulator terminating,err=%v", err)
		os.Exit(1)
	}

	return out, err
}
func (inst *instance) python(args ...string) ([]byte, error) {
	if inst.debug {
		Logf(0, "running command: python %+v", args)
	}
	rpipe, wpipe, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe: %v", err)
	}
	defer wpipe.Close()
	defer rpipe.Close()
	cmd := exec.Command("/usr/bin/python", args...)
	cmd.Stdout = wpipe
	cmd.Stderr = wpipe
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wpipe.Close()
	done := make(chan bool)
	go func() {
		select {
		case <-time.After(120 * time.Second):
			Logf(0, "adb hanged (Oops)")
			cmd.Process.Kill()
		case <-done:
		}
	}()
	if err := cmd.Wait(); err != nil {
		close(done)
		out, _ := ioutil.ReadAll(rpipe)
		if inst.debug {
			Logf(0, "python failed: %v\n%s", err, out)
		}
		os.Exit(1)
		return nil, fmt.Errorf("python %+v failed: %v\n%s", args, err, out)
	}
	close(done)
	if inst.debug {
		Logf(0, "python returned")
	}
	out, _ := ioutil.ReadAll(rpipe)
	Logf(0, "out is %v", out)
	return out, nil
}

//Charm end
func (inst *instance) phoneadb(args ...string) ([]byte, error) {
	args = append([]string{"-s", inst.Device_id}, args...)
	if inst.debug {
		Logf(0, "running command: adb %+v", args)
	}
	rpipe, wpipe, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe: %v", err)
	}
	defer wpipe.Close()
	defer rpipe.Close()
	cmd := exec.Command(inst.cfg.AdbBin, args...)
	cmd.Stdout = wpipe
	cmd.Stderr = wpipe
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wpipe.Close()
	done := make(chan bool)
	go func() {
		select {
		case <-time.After(120 * time.Second):
			Logf(0, "adb hanged (Oops)")
			cmd.Process.Kill()
		case <-done:
		}
	}()
	if err := cmd.Wait(); err != nil {
		close(done)
		out, _ := ioutil.ReadAll(rpipe)
		if inst.debug {
			Logf(0, "adb failed: %v\n%s", err, out)
		}
		////os.Exit(1)
		return nil, fmt.Errorf("adb %+v failed: %v\n%s", args, err, out)
	}
	close(done)
	if inst.debug {
		Logf(0, "adb returned")
	}
	out, _ := ioutil.ReadAll(rpipe)
	return out, nil
}

//Ardalan start
/*
 * TODO: we might want to kill the adbserver and try again if it's not successful
 * after all iterations of the for loop.
 */
func (inst *instance) Phoneadb(args ...string) ([]byte, error) {
	var out []byte
	var err error
	for i := 0; i < 10; i++ {
		out, err = inst.phoneadb(args...)
		if err != nil {
			out2, err2 := exec.Command("killall", "adb").Output()
			if err2 != nil {
				Logf(0, "killall adb failed, err = %v, out = %v", err2, out2)
			}
			time.Sleep(10 * time.Second)
			continue
		}

		break
	}

	if err != nil {
		Logf(0, "ERROR: Could not adb to the phone terminating,err=%v", err)
		os.Exit(1)
	}

	return out, err
}

//Charm  start
////func (inst *instance) repair() error {
////	// Assume that the device is in a bad state initially and reboot it.
////	// Ignore errors, maybe we will manage to reboot it anyway.
////	//inst.waitForSsh()
////	// History: adb reboot episodically hangs, so we used a more reliable way:
////	// using syz-executor to issue reboot syscall. However, this has stopped
////	// working, probably due to the introduction of seccomp. Therefore,
////	// we revert this to `adb shell reboot` in the meantime, until a more
////	// reliable solution can be sought out.
////	//Hamid
////	if _, err := inst.adb("emu", "kill"); err != nil {
////		return err
////	}
////	//Hamid Temp
////	os.Exit(1)
////	// we have to reboot the phone too :D
////	//if _, err := inst.Phoneadb("reboot"); err != nil {
////	//	return fmt.Errorf("adb reboot failed: %v", err)
////	//}
////	//if _, err := inst.Phoneadb("wait-for-device"); err != nil {
////	//	return fmt.Errorf("adb wait-for-device failed: %v", err)
////	//}
////	//time.Sleep(30 * time.Second)
////	//if _, err := inst.Phoneadb("remount"); err != nil {
////	//	return fmt.Errorf("adb remount failed: %v", err)
////	//}
////	//if _, err := inst.Phoneadb("root"); err != nil {
////	//	return fmt.Errorf("adb root failed: %v", err)
////	//}
////	//inst.Phoneadb("shell", "rm", "-f", "/data/local/tmp/enable_usb_accessory.sh")
////	//if _, err := inst.Phoneadb("push", "/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/enable_usb_accessory.sh", "/data/local/tmp/enable_usb_accessory.sh"); err != nil {
////	//	return fmt.Errorf("adb push enable_usb_accessory.sh /data/local/tmp/enable_usb_accessory.sh failed: %v", err)
////	//}
////	//if _, err := inst.Phoneadb("shell", "chmod", "a+x", "/data/local/tmp/enable_usb_accessory.sh"); err != nil {
////	//	return fmt.Errorf("adb shell 'chmod a+x /data/local/tmp/enable_usb_accessory.sh' failed: %v", err)
////	//}
////	//if _, err := inst.Phoneadb("shell", "source /data/local/tmp/enable_usb_accessory.sh"); err != nil {
////	//	return fmt.Errorf("adb shell /data/local/tmp/enable_usb_accessory.sh failed: %v", err)
////	//}
////	//Logf(0, "Phone is ready:%v", inst.cfg.Device_id)
////	//time.Sleep(30 * time.Second)
////	//dev_file, err := exec.Command("/usr/bin/python", "/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py", inst.Phone_id).Output()
////	//if err != nil {
////	//	return fmt.Errorf("/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py failed: %v", err)
////	//}
////	//Logf(0, "out is %s", dev_file)
////	// Now give it another 5 minutes to boot.
////	inst.Boot()
////	//if _, err := inst.adb("shell", "reboot"); err != nil {
////	//	return err
////	//}
////	//if !vmimpl.SleepInterruptible(10 * time.Second) {
////	//	return fmt.Errorf("shutdown in progress")
////	//}
////	//if err := inst.waitForSsh(); err != nil {
////	//	return err
////	//}
////	// Switch to root for userdebug builds.
////	//inst.adb("root")
////	//if err := inst.waitForSsh(); err != nil {
////	//	return err
////	//}
////	return nil
////}
//Charm end

/* Copy file via adb push */
func (inst *instance) Copy(hostSrc string) (string, error) {
	//Charm
	//since we could not push large static binary files to Our VM
	//We have put the binaries in /system/bin and here we just make the soft link to them
	if (strings.Contains(hostSrc, "syz-fuzzer")) || (strings.Contains(hostSrc, "syz-executor")) || (strings.Contains(hostSrc, "syz-execprog")) {
		vmDst := filepath.Join("/data/local/tmp", filepath.Base(hostSrc))
		smDst := filepath.Join("/system/bin", filepath.Base(hostSrc))
		////if _, err := inst.adb("push", hostSrc, vmDst); err != nil {
		inst.adb("shell", "rm", "-f", vmDst)

		if _, err := inst.adb("shell", "ln", "-s", smDst, vmDst); err != nil {
			return "", err
		}
		return vmDst, nil
	} else {

		vmDst := filepath.Join("/data/local/tmp", filepath.Base(hostSrc))
		if _, err := inst.adb("push", hostSrc, vmDst); err != nil {
			return "", err
		}
		return vmDst, nil

	}
}

/* Run command via adb shell */
func (inst *instance) Run(timeout time.Duration, stop <-chan bool, command string) (<-chan []byte, <-chan error, error) {

	//Charm start
	inst.terminate = make(chan bool)
	inst.terminated = false
	//Charm end
	adbRpipe, adbWpipe, err := osutil.LongPipe()
	if err != nil {
		//Charm start
		////inst.qemu.Process.Kill()
		//exec.Command("adb", "-s", inst.device, "emu", "kill").Start()
		if !inst.terminated {
			inst.terminate <- true
		}
		//Charm end
		// Do not close the inst.rpipe 'cause it will be closed in merger
		// inst.rpipe.Close()
		// inst.rpipe = nil
		return nil, nil, err
	}

	if inst.debug {
		Logf(0, "running command: adb -s %v shell %v", inst.device, command)
	}

	adb := exec.Command("adb", "-s", inst.device, "shell", "cd /data/local/tmp; echo '"+command+"' > daemon.sh; chmod +x /data/local/tmp/daemon.sh; daemonize /data/local/tmp/daemon.sh &")
	adb.Stdout = adbWpipe
	adb.Stderr = adbWpipe
	if err := adb.Start(); err != nil {
		//Charm start
		if !inst.terminated {
			inst.terminate <- true
		}
		//Charm end
		// Do not close the inst.rpipe 'cause it will be  closed in merger
		// inst.rpipe.Close()
		adbRpipe.Close()
		adbWpipe.Close()
		return nil, nil, fmt.Errorf("failed to start adb: %v", err)
	}
	adbWpipe.Close()
	adbDone := make(chan error, 1)
	go func() {
		err := adb.Wait()
		if inst.debug {
			Logf(0, "adb exited: %v", err)
		}
		//Charm: we're running the syz-fuzzer as a daemon now
		////adbDone <- fmt.Errorf("adb exited: %v", err)
	}()

	//Charm start
	usbDone := make(chan error, 1)
	go func() {
		for {
			time.Sleep(5 * time.Second)
			dev_file, err := exec.Command("/usr/bin/python", "/home/charm/Hamid/fuzzing/gopath/src/github.com/google/syzkaller/find_dev_file.py", inst.Device_id).Output()
			dev_file_string := fmt.Sprintf("%s", dev_file)
			if err == nil && dev_file_string != inst.usb_dev_file {
				/* Most likely the phone has crashed */
				usbDone <- fmt.Errorf("Charm USB Error")
				break
			}
		}
	}()
	//Charm end

	inst.merger.Add("adb", adbRpipe)

	errc := make(chan error, 1)
	signal := func(err error) {
		select {
		case errc <- err:
		default:
		}
	}

	go func() {
		select {
		case <-time.After(timeout):
			inst.terminated = true
			//Charm start
			////signal(vmimpl.TimeoutErr)
			signal(vmimpl.ErrTimeout)
			//Charm end
			exec.Command("adb", "-s", inst.device, "emu", "kill").Start()
		case <-stop:
			inst.terminated = true
			//Charm
			signal(vmimpl.ErrTimeout)
			//Charm start
			////inst.qemu.Process.Kill()
			exec.Command("adb", "-s", inst.device, "emu", "kill").Start()
			////adb.Process.Kill()
			//Charm end
		case err := <-adbDone:
			inst.terminated = true
			signal(err)
			//Charm start
			////inst.qemu.Process.Kill()
			exec.Command("adb", "-s", inst.device, "emu", "kill").Start()
			//Charm end
		//Charm start
		case <-inst.terminate:
			inst.terminated = true
			exec.Command("adb", "-s", inst.device, "emu", "kill").Start()
		case <-usbDone:
			inst.terminated = true
			signal(vmimpl.CharmErr)
			exec.Command("adb", "-s", inst.device, "emu", "kill").Start()
		}
		//Charm end
		// Waiting on merger will close the channel
		// inst.merger.Wait()
	}()
	return inst.merger.Output, errc, nil
}

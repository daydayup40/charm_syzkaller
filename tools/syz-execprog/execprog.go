// Copyright 2015 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

// execprog executes a single program or a set of programs
// and optionally prints information about execution.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/google/syzkaller/pkg/cover"
	"github.com/google/syzkaller/pkg/ipc"
	"github.com/google/syzkaller/pkg/log"
	"github.com/google/syzkaller/pkg/osutil"
	"github.com/google/syzkaller/prog"
	_ "github.com/google/syzkaller/sys"
)

var (
	flagOS        = flag.String("os", runtime.GOOS, "target os")
	flagArch      = flag.String("arch", runtime.GOARCH, "target arch")
	flagCoverFile = flag.String("coverfile", "", "write coverage to the file")
	flagRepeat    = flag.Int("repeat", 1, "repeat execution that many times (0 for infinite loop)")
	flagProcs     = flag.Int("procs", 1, "number of parallel processes to execute programs")
	flagOutput    = flag.String("output", "none", "write programs to none/stdout")
	flagFaultCall = flag.Int("fault_call", -1, "inject fault into this call (0-based)")
	flagFaultNth  = flag.Int("fault_nth", 0, "inject fault on n-th operation (0-based)")
	flagHints     = flag.Bool("hints", false, "do a hints-generation run")
)

//Charm start
var charmlog_file, err11 = os.OpenFile("/sys/kernel/charmlog/charmlog", os.O_RDWR, 0755)

func Logf_charm(v int, msg string, args ...interface{}) {
	timeStr := ""
	timeStr = time.Now().Format("2006/01/02 15:04:05 ")
	msg2 := fmt.Sprintf(timeStr+msg+"\n", args...)
	fmt.Fprintf(charmlog_file, msg2)
}

func Printf_charm(msg string, args ...interface{}) {
	msg2 := fmt.Sprintf(msg, args...)
	fmt.Fprintf(charmlog_file, msg2)
}

//Charm end
func main() {
	//Charm start
	charmlog_file, err11 = os.OpenFile("/sys/kernel/charmlog/charmlog", os.O_RDWR, 0755)
	if charmlog_file == nil {
		log.Logf(0, "charmlog_file is nil")
	}
	if err11 != nil {
		log.Logf(0, "err11 is %v", err11)
	}
	//Charm  end
	flag.Parse()
	if len(flag.Args()) == 0 {
		Printf_charm("usage: execprog [flags] file-with-programs+\n")
		fmt.Fprintf(os.Stderr, "usage: execprog [flags] file-with-programs+\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	target, err := prog.GetTarget(*flagOS, *flagArch)
	if err != nil {
		Printf_charm("%v", err)
		log.Fatalf("%v", err)
	}

	entries := loadPrograms(target, flag.Args())
	if len(entries) == 0 {
		return
	}

	config, execOpts := createConfig(entries)

	var wg sync.WaitGroup
	wg.Add(*flagProcs)
	var posMu, logMu sync.Mutex
	gate := ipc.NewGate(2**flagProcs, nil)
	var pos int
	var lastPrint time.Time
	shutdown := make(chan struct{})
	for p := 0; p < *flagProcs; p++ {
		pid := p
		go func() {
			defer wg.Done()
			env, err := ipc.MakeEnv(config, pid)
			if err != nil {
				Printf_charm("failed to create ipc env: %v", err)
				log.Fatalf("failed to create ipc env: %v", err)
			}
			defer env.Close()
			for {
				if !func() bool {
					// Limit concurrency window.
					ticket := gate.Enter()
					defer gate.Leave(ticket)

					posMu.Lock()
					idx := pos
					pos++
					if idx%len(entries) == 0 && time.Since(lastPrint) > 5*time.Second {
						Logf_charm(0, "executed programs: %v", idx)
						////log.Logf(0, "executed programs: %v", idx)
						lastPrint = time.Now()
					}
					posMu.Unlock()
					if *flagRepeat > 0 && idx >= len(entries)**flagRepeat {
						return false
					}
					entry := entries[idx%len(entries)]
					callOpts := execOpts
					if *flagFaultCall == -1 && entry.Fault {
						newOpts := *execOpts
						newOpts.Flags |= ipc.FlagInjectFault
						newOpts.FaultCall = entry.FaultCall
						newOpts.FaultNth = entry.FaultNth
						callOpts = &newOpts
					}
					switch *flagOutput {
					case "stdout":
						strOpts := ""
						if callOpts.Flags&ipc.FlagInjectFault != 0 {
							strOpts = fmt.Sprintf(" (fault-call:%v fault-nth:%v)", callOpts.FaultCall, callOpts.FaultNth)
						}
						data := entry.P.Serialize()
						logMu.Lock()
						Logf_charm(0, "executing program %v%v:\n%s", pid, strOpts, data)
						//log.Logf(0, "executing program %v%v:\n%s", pid, strOpts, data)
						logMu.Unlock()
					}
					output, info, failed, hanged, err := env.Exec(callOpts, entry.P)
					select {
					case <-shutdown:
						return false
					default:
					}
					if failed {
						Logf_charm(0, "BUG: executor-detected bug:\n%s", output)
						////log.Logf(0, "BUG: executor-detected bug:\n%s", output)
					}
					if config.Flags&ipc.FlagDebug != 0 || err != nil {
						Logf_charm(0, "result: failed=%v hanged=%v err=%v\n\n%s",
							////log.Logf(0, "result: failed=%v hanged=%v err=%v\n\n%s",
							failed, hanged, err, output)
					}
					if len(info) != 0 {
						Logf_charm(1, "RESULT: signal %v, coverage %v errno %v",
							////log.Logf(1, "RESULT: signal %v, coverage %v errno %v",
							len(info[0].Signal), len(info[0].Cover), info[0].Errno)
					} else {
						Logf_charm(1, "RESULT: no calls executed")
						////log.Logf(1, "RESULT: no calls executed")
					}
					if *flagCoverFile != "" {
						for i, inf := range info {
							Logf_charm(0, "call #%v: signal %v, coverage %v",
								////log.Logf(0, "call #%v: signal %v, coverage %v",
								i, len(inf.Signal), len(inf.Cover))
							if len(inf.Cover) == 0 {
								continue
							}
							buf := new(bytes.Buffer)
							for _, pc := range inf.Cover {
								fmt.Fprintf(buf, "0x%x\n", cover.RestorePC(pc, 0xffffffff))
							}
							err := osutil.WriteFile(fmt.Sprintf("%v.%v", *flagCoverFile, i), buf.Bytes())
							if err != nil {
								Printf_charm("failed to write coverage file: %v", err)
								log.Fatalf("failed to write coverage file: %v", err)
							}
						}
					}
					if *flagHints {
						ncomps, ncandidates := 0, 0
						for i := range entry.P.Calls {
							if *flagOutput == "stdout" {
								Printf_charm("call %v:\n", i)
								fmt.Printf("call %v:\n", i)
							}
							comps := info[i].Comps
							for v, args := range comps {
								ncomps += len(args)
								if *flagOutput == "stdout" {
									Printf_charm("comp 0x%x:", v)
									fmt.Printf("comp 0x%x:", v)
									for arg := range args {
										Printf_charm(" 0x%x", arg)
										fmt.Printf(" 0x%x", arg)
									}
									Printf_charm("\n")
									fmt.Printf("\n")
								}
							}
							entry.P.MutateWithHints(i, comps, func(p *prog.Prog) {
								ncandidates++
								if *flagOutput == "stdout" {
									Logf_charm(1, "PROGRAM:\n%s", p.Serialize())
									////log.Logf(1, "PROGRAM:\n%s", p.Serialize())
								}
							})
						}
						Logf_charm(0, "ncomps=%v ncandidates=%v", ncomps, ncandidates)
						////log.Logf(0, "ncomps=%v ncandidates=%v", ncomps, ncandidates)
					}
					return true
				}() {
					return
				}
			}
		}()
	}

	osutil.HandleInterrupts(shutdown)
	wg.Wait()
}

func loadPrograms(target *prog.Target, files []string) []*prog.LogEntry {
	var entries []*prog.LogEntry
	for _, fn := range files {
		data, err := ioutil.ReadFile(fn)
		if err != nil {
			log.Fatalf("failed to read log file: %v", err)
			Printf_charm("failed to read log file: %v", err)
		}
		entries = append(entries, target.ParseLog(data)...)
	}
	Logf_charm(0, "parsed %v programs", len(entries))
	////log.Logf(0, "parsed %v programs", len(entries))
	return entries
}

func createConfig(entries []*prog.LogEntry) (*ipc.Config, *ipc.ExecOpts) {
	config, execOpts, err := ipc.DefaultConfig()
	if err != nil {
		log.Fatalf("%v", err)
		Printf_charm("%v", err)
	}
	if config.Flags&ipc.FlagSignal != 0 {
		execOpts.Flags |= ipc.FlagCollectCover
	}
	if *flagCoverFile != "" {
		config.Flags |= ipc.FlagSignal
		execOpts.Flags |= ipc.FlagCollectCover
		execOpts.Flags &^= ipc.FlagDedupCover
	}
	if *flagHints {
		if execOpts.Flags&ipc.FlagCollectCover != 0 {
			execOpts.Flags ^= ipc.FlagCollectCover
		}
		execOpts.Flags |= ipc.FlagCollectComps
	}
	if *flagFaultCall >= 0 {
		config.Flags |= ipc.FlagEnableFault
		execOpts.Flags |= ipc.FlagInjectFault
		execOpts.FaultCall = *flagFaultCall
		execOpts.FaultNth = *flagFaultNth
	}
	handled := make(map[string]bool)
	for _, entry := range entries {
		for _, call := range entry.P.Calls {
			handled[call.Meta.CallName] = true
		}
	}
	if handled["syz_emit_ethernet"] || handled["syz_extract_tcp_res"] {
		config.Flags |= ipc.FlagEnableTun
	}
	return config, execOpts
}

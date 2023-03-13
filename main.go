package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/urfave/cli"
)

var (
	serviceName string
	consulAddr  string
	envName     string
	lastGroups  map[string][]string = make(map[string][]string)
	tmpl        *template.Template
	locker      sync.Mutex
)

type Data struct {
	Data []Item
	Port int
}

type Item struct {
	Name  string
	Addrs []string
}

func init() {
	var err error
	bs, err := ioutil.ReadFile("./nginx.conf")
	if err != nil {
		panic(err)
	}
	tmpl, err = template.New("test").Parse(string(bs))
	if err != nil {
		panic(err)
	}
}

func main() {
	ca := cli.NewApp()
	ca.Name = "nginx agent"
	ca.Version = "0.0.1"
	ca.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "addr",
			Usage:       "consul addr",
			Value:       "172.16.0.179:8500",
			Destination: &consulAddr,
		},
		cli.StringFlag{
			Name:        "service",
			Usage:       "service name",
			Value:       "banban/nginx",
			Destination: &serviceName,
		},
		cli.StringFlag{
			Name:        "env",
			Usage:       "env name",
			Value:       "prod",
			Destination: &envName,
		},
	}
	ca.Action = func(c *cli.Context) error {
		if len(consulAddr) == 0 {
			return fmt.Errorf("empty consul addr")
		}
		ip := net.ParseIP(consulAddr)
		if ip != nil {
			return fmt.Errorf("error consul addr %s", ip.String())
		}
		log.Printf("start with addr %s, service %s\n", consulAddr, serviceName)
		return nil
	}
	err := ca.Run(os.Args)
	if err != nil {
		panic(err)
	}
	go listen()
	t := time.NewTicker(time.Second * 3)
	q := time.NewTicker(time.Second * 60)
	for {
		select {
		case <-t.C:
			log.Println("timer", lastGroups)
			break
		case <-q.C:
			query()
			break
		}
	}
}

func listen() {
	client, err := getClient()
	if err != nil {
		panic(err)
	}
	var lastIndex uint64 = 0
	//查询同时存在nginx和envName
	var tags []string = []string{"nginx"}
	if len(envName) > 0 {
		tags = append(tags, envName)
	}
	for {
		services, metainfo, err := client.Health().ServiceMultipleTags(serviceName, tags, true, &consulapi.QueryOptions{
			WaitIndex: lastIndex, // 同步点，这个调用将一直阻塞，直到有新的更新
			WaitTime:  time.Second * 60,
		})
		if err != nil || metainfo.LastIndex == lastIndex {
			log.Println("consul error or timeout", err)
			time.Sleep(time.Second)
			continue
		}
		lastIndex = metainfo.LastIndex
		if !parseServices(services) {
			lastIndex = 0
			time.Sleep(time.Second * 3)
		}
	}
}

func query() {
	client, err := getClient()
	if err != nil {
		panic(err)
	}
	services, _, err := client.Health().Service(serviceName, "nginx", true, &consulapi.QueryOptions{})
	if err != nil {
		panic(err)
	}
	parseServices(services)
}

func parseServices(services []*consulapi.ServiceEntry) bool {
	locker.Lock()
	defer locker.Unlock()
	groups := map[string][]string{}
	for _, service := range services {
		if service.Service.Service == serviceName {
			tags := service.Service.Tags
			for _, tag := range tags {
				if tag == "nginx" || tag == envName {
					continue
				}
				if len(tag) < 3 || !strings.HasPrefix(tag, "/") || !strings.HasSuffix(tag, "/") || strings.Count(tag, "/") != 2 {
					continue
				}
				if _, ok := groups[tag]; !ok {
					groups[tag] = make([]string, 0)
				}
				groups[tag] = append(groups[tag], service.Service.ID)
			}
		}
	}
	if len(groups) > 0 {
		//去重下，因为注册时，同一个机器可能在多个节点注册
		for tag, vals := range groups {
			data := []string{}
			exist := map[string]bool{}
			for _, val := range vals {
				if _, ok := exist[val]; !ok {
					exist[val] = true
					data = append(data, val)
				}
			}
			groups[tag] = data
		}
		changed := false
		if len(groups) != len(lastGroups) {
			changed = true
		} else {
			//获取双方的keys
			keys := getMapKeys(groups)
			lastKeys := getMapKeys(lastGroups)
			if !isSliceSame(keys, lastKeys) {
				changed = true
			} else {
				//对比每个key下面的[]string
				for key := range groups {
					//理论上不用判断
					if _, ok := lastGroups[key]; !ok {
						changed = true
						break
					}
					if !isSliceSame(groups[key], lastGroups[key]) {
						changed = true
						break
					}
				}
			}
		}
		if changed {
			log.Println("consul changed", groups)
			err := makeConf(groups)
			if err == nil {
				lastGroups = groups
			} else {
				log.Println("make error", err, "wait from 3s")
				return false
			}
		} else {
			log.Println("consul changed, but local no change")
		}
	} else {
		log.Println("consul changed, but no addr")
	}
	return true
}

func getClient() (*consulapi.Client, error) {
	config := consulapi.DefaultConfig()
	config.Address = consulAddr
	config.Scheme = "http"
	return consulapi.NewClient(config)
}

func getMapKeys(a map[string][]string) []string {
	keys := []string{}
	for key := range a {
		keys = append(keys, key)
	}
	return keys
}

func isSliceSame(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	var as sort.StringSlice = a
	var bs sort.StringSlice = b
	sort.Sort(as)
	sort.Sort(bs)
	for i := 0; i < len(as); i++ {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func makeConf(conf map[string][]string) error {
	if len(conf) == 0 {
		return nil
	}
	//生成nginx配置文件
	data := &Data{
		Port: 9000,
		Data: []Item{},
	}
	for tag, addrs := range conf {
		data.Data = append(data.Data, Item{
			Name:  tag[1 : len(tag)-1],
			Addrs: addrs,
		})
	}

	var buf bytes.Buffer
	err := tmpl.Execute(&buf, data)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile("/etc/nginx/sites-enabled/nginx.proxy.conf", buf.Bytes(), 0666)
	if err != nil {
		return err
	}
	//reload nginx
	//ubuntu 默认安装位置
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("brew", "services", "reload", "nginx")
	} else if runtime.GOOS == "linux" {
		cmd = exec.Command("/usr/sbin/nginx", "-s", "reload")
	} else {
		return fmt.Errorf("unknow os type")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	log.Println("nginx output\n", string(out))
	return nil
}

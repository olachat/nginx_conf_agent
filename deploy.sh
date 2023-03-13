#!/bin/bash  

path="/home/admin/app"
agentPath="/home/webroot/nginx_agent"
logPath="/home/log"

if [ ! -d "$agentPath" ]; then
	mkdir -p "$agentPath"
fi

if [ ! -d "$logPath" ]; then
	mkdir -p "$logPath"
fi


files=("agent" "nginx.conf")
for i in ${!files[@]}; do
	#复制可执行文件到目标目录
	cp -f "${path}/${files[i]}" "${agentPath}/${files[i]}"
	if [ $? -ne 0 ]; then
		echo "error to copy agent to target";
		exit 1
	fi
done



#copy supervisor 配置文件
if [ ! -f "/etc/supervisor/conf.d/nginx.agent.conf" ];then
	cp -f "${path}/nginx.agent.conf" /etc/supervisor/conf.d/nginx.agent.conf
	#更新 supervisor 配置
	#系统会自动启动进程
	supervisorctl update nginx.agent
else
	#重启进程
	supervisorctl restart nginx.agent
fi

if [ $? -ne 0 ]; then
	echo "error with supervisorctl";
	exit 1
fi


# 判断进程状态
for i in {1..10}
do
	sleep 1
	v=`supervisorctl status nginx.agent | grep "Exited" | wc -l`
	if [ $v != "0" ]; then
		echo "error status with nginx.agent";
		exit 1
	else
		echo "check status ${i} ok"
	fi
done

echo "ok"
exit 0



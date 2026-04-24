# portmap
Map localhost ports to names.

## usage
list all ports like `netstat -pant` or `lsof -Pn -iTCP -sTCP:LISTEN`
```
$ source <(portmap completion bash)
$ portmap ls
22     ssh
53     dns
80     caddy-plain
443    caddy-tls
```

label a port
```
portmap 8080 django-runserver
```

list labeled ports
```
$ portmap ls
22     ssh
53     dns
80     caddy
80     caddy-plain
8080   django-runserver
```

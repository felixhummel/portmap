# felix-portmap
Map localhost ports to names.

## usage
list all ports like `netstat -pant` or `lsof -Pn -iTCP -sTCP:LISTEN`
```
$ PAGER=cat portmap -l
+-------+-------------+------------+---------+------------------------+
|  PORT | NAME        | INGRESS    | PID     | PROCESS                |
+-------+-------------+------------+---------+------------------------+
|    22 | ssh         | no-ingress |         |                        |
|    53 | dns         | ingress    |         |                        |
|    80 | caddy-plain | ingress    |         | docker:ingress-caddy-1 |
|   443 | caddy-tls   | ingress    |         | docker:ingress-caddy-1 |
|   631 | cupsd       | ingress    |         |                        |
|  1716 | kde-connect | ingress    | 2342    | kdeconnectd            |
|  2019 | caddy-admin | ingress    |         | docker:ingress-caddy-1 |
|  5092 |             |            |         | docker:parakeet-cpu    |
|  5173 |             |            | 505130  | node-MainThread        |
|  5174 |             |            | 966989  | node-MainThread        |
|  6188 |             |            | 721936  | cef_server             |
|  6189 |             |            | 691551  | pycharm                |
|  8384 |             |            | 2509    | syncthing              |
|  8765 |             |            | 3626605 | python3                |
| 36373 |             |            | 3634871 | chrome                 |
| 36717 |             |            | 698738  | embeddings-serv        |
| 48025 |             |            | 305200  | spotify                |
| 57621 |             |            | 305200  | spotify                |
+-------+-------------+------------+---------+------------------------+
```

Use `--verbose` to get more information.

label a port
```
portmap 8080 django-runserver
```

list labeled ports
```
$ portmap
22     ssh                no-ingress
53     dns                ingress
80     caddy              ingress
80     caddy-plain        ingress
443    caddy-tls          ingress
1716   kde-connect        ingress
2019   caddy-admin        ingress
8080   django-runserver   ingress
```

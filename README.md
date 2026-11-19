<p align="center"><img src="docs/logo.svg" alt="webye" width="305"></p>

# webye

A privacy-friendly frontend for [CeX](https://es.webuy.com/) Spain. Search
products and see what CeX sells them for and what it pays for them, without
needing JavaScript, without trackers and without your browser ever talking
to CeX.

This repository is the source code of a private instance, built for personal
use. No public instance is offered. Issues are welcome.

## Features

- Search the whole CeX catalogue with the same filter as its website (for
  sale and in stock), 170 results per page.
- Every result shows its price and what CeX pays for it, in cash or in
  store credit.
- Browse by section, product line and category, with the same pages and
  links as the CeX website.
- Sort results with the same options as the CeX website: relevance,
  popularity, price, name or rating.
- Filter with the same lists as the CeX website: stores, category, price,
  sold-out products and every product attribute it shows (PEGI rating,
  grade, condition…). Filtered CeX links work as they are.
- Three filters the CeX website does not have: products sold only in
  stores, discontinued products (the ones CeX no longer buys) and a range
  for the cash CeX pays.
- Product pages with specifications, description, rating and online or
  in-store stock.
- Stock in every store, with Click & Collect and today's opening hours. It
  is asked of CeX only when you open it, as its website does.
- Price data the CeX website does not show: the previous price, the launch
  price and the date of the last price change.
- The same paths as CeX: swap the domain of a CeX link for webye's and you
  land on the same search or product.
- Products without a photo get webye's own placeholder instead of CeX's
  generic picture.
- Ethical JavaScript: everything works with JavaScript off. The only
  script, which opens the store stock panel without reloading the page, is
  free software (AGPL-3.0, tagged for GNU LibreJS), unminified and served
  by webye itself.
- Your browser never contacts CeX or any third party: styles, fonts, icons
  and photos are served by the instance itself.
- No cookies and no trackers. Searches carry no user token and no click
  analytics.
- Nothing is stored: no database and no files, only an in-memory cache that
  lasts as long as CeX allows itself on its own website.
- Lightweight: a results page asks CeX for about 124 KB, against the 539 KB
  its website requests.
- Polite to CeX: repeated requests are collapsed, each visitor is rate
  limited, and webye stops calling on its own when CeX pushes back.
- A `/salud` page for uptime monitors (see [Health checks](#health-checks)).

The interface is in Spanish, like the CeX website it mirrors. Only the
Spanish market is supported: CeX does not sell across borders.

## How it works

webye is a single Go binary with no dependencies outside the standard
library. Templates, styles, fonts and the script are embedded in it. It
talks to the same three CeX services the website uses:

| Service | Used for | Kept in the cache |
|---|---|---|
| Algolia, through CeX's own proxy (`search.webuy.io`) | searches, filters and their counts | 1 hour |
| `wss2.cex.es.webuy.io` | product pages and stock in stores | 5 minutes |
| | a product line's categories | 30 minutes |
| | sections and product lines | 24 hours |
| `es.static.webuy.com` | product photos | as long as CeX's `max-age` |

The lifetimes are the ones CeX allows itself in its own service worker.
The cache lives in memory (16 MiB) and is lost on restart.

Every request to CeX goes through one guardian per service:

- Identical requests in flight are collapsed into one.
- No more than 6 requests at once per service, as a browser would open.
- A 403, a 429, a 5xx or a challenge page pauses that service: for as long
  as `Retry-After` says, or one minute doubling up to one hour. While
  paused, webye serves what it has cached and asks CeX nothing.
- Responses over 1 MiB are refused.

Search and product pages are rate limited per visitor: 30 pages in a row,
refilled at 30 per minute. Photos have their own limit: 340 in a row (two
pages of results), refilled at 170 per minute. Over a limit, webye answers
429 and nothing reaches CeX.

The sections bar in the header is fetched once at start-up and then once a
day in the background, so no page waits for it.

## Running it

### Requirements

- Docker, or Go 1.26 or later.
- Outbound HTTPS to `search.webuy.io`, `wss2.cex.es.webuy.io` and
  `es.static.webuy.com`.
- No API key. Searches go through CeX's own Algolia proxy,
  `search.webuy.io`, which takes none, the same way the CeX website
  searches. There is no search key to find or keep up to date.

### With Docker

```sh
git clone https://github.com/darlopvil/webye.git
cd webye
cp .env.example .env && chmod 600 .env
cp docker-compose.example.yml docker-compose.yml
docker compose up -d --build
```

The image is built from `scratch` and runs as an unprivileged user. The
example compose file runs it read-only, with every capability dropped, a
32 MiB Go memory limit (`GOMEMLIMIT`) and a 64 MB container limit.

It publishes no ports: it expects a reverse proxy that reaches the
container by name on a shared Docker network (`proxy_network` in the
example; rename it to yours). That network cannot be `internal`, because
webye needs outbound HTTPS to CeX. To try it without a proxy, uncomment the
`ports:` lines.

### Without Docker

With Go 1.26 or later:

```sh
go build -trimpath -o webye .
./webye -p 8080
```

### Configuration

| Variable | Flag | Default | |
|---|---|---|---|
| `WEBYE_HOST` | `-h` | `0.0.0.0` | Listen address |
| `WEBYE_PORT` | `-p` | `8080` | Listen port |

Flags win over variables.

### Routes

| Route | |
|---|---|
| `/` | home page; it never talks to CeX |
| `/search`, `/search/` | search, with CeX's own parameters (`stext`, `page`, `productLineId`, `categoryIds`, filters) plus webye's `sort`, `storeOnly`, `discontinued` and `cashPriceCalculated` |
| `/supercat?superCatId=…` | the product lines of a section |
| `/product-detail?id=…` | product page; `&stores=1` adds the stock in stores |
| `/product-detail/stores?id=…` | the stock in stores alone, for the script |
| `/img/…` | product photos, fetched from CeX by webye |
| `/static/…` | styles, fonts, icons and the script |
| `/salud` | health of the CeX services, see [Health checks](#health-checks) |
| `/about`, `/robots.txt`, `/favicon.ico` | |

### Behind a reverse proxy

webye serves plain HTTP and leaves TLS to the proxy. It sets its own
security headers, including a strict Content Security Policy, so the proxy
does not need to add any.

The rate limit tells visitors apart by IP. webye trusts the `X-Real-IP`
header only when the connection comes from a private or loopback address,
so the proxy must set it (nginx: `proxy_set_header X-Real-IP
$remote_addr;`). Without it, every visitor shares the proxy's address and
its limit. IPv6 visitors are grouped by their /64.

`robots.txt` asks every crawler to stay away.

## Health checks

`/salud` reports each CeX service from its last real request. It never
makes a request of its own, so monitors can check it as often as they like
without costing CeX anything.

It answers 200 while everything works, including before the first request,
and 503 when a service is paused after a block or its last request failed,
for example because an API changed shape. `/salud?destino=algolia`, `wss2`
or `imagenes` checks one service only, so each can have its own monitor.

Each line also counts the requests since start-up and how long they took.

To check from the server that webye gets the same answers as the CeX
website, run one real search through the guardian:

```sh
docker exec webye_app /webye -probe "tomb raider"
```

It prints the status, size and time of the response, the parameters
Algolia applied and the first product.

## Development

```sh
go vet ./...
go test ./...
```

The tests run against local fake servers built from trimmed copies of real
CeX responses; they never reach CeX.

Identifiers are in Spanish and comments in English. Each file holds one
part of the site:

| File | |
|---|---|
| `main.go` | routes and the HTTP server |
| `guardian.go` | requests to CeX: cache, collapsing, pauses and timings |
| `limites.go` | per-visitor rate limit |
| `busqueda.go`, `filtros.go`, `resultados.go` | search, filters and the results page |
| `secciones.go` | sections, product lines and categories |
| `ficha.go`, `saneado.go` | product pages and their sanitised description |
| `tiendas.go` | stock in stores |
| `imagenes.go`, `estaticos.go` | product photos and static files |
| `cabeceras.go` | security headers and `robots.txt` |
| `salud.go`, `sonda.go` | `/salud` and `-probe` |
| `vistas.go`, `templates/` | rendering |
| `static/` | styles, fonts, icons and the stock panel script |

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE).

The bundled fonts keep their own licenses, next to them in `static/fuentes/`:
[Anton](https://github.com/googlefonts/AntonFont) (SIL Open Font License 1.1)
and [Permanent Marker](https://fonts.google.com/specimen/Permanent+Marker)
(Apache License 2.0).

webye is an independent project and is not affiliated with, endorsed by or
connected to CeX (Complete Entertainment Exchange), and uses none of its
logos: the one in `docs/` is webye's own.

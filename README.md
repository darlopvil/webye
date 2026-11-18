# webye

A privacy-friendly frontend for [CeX](https://es.webuy.com/). Search products
and see what CeX sells them for and what it pays for them, without
JavaScript, without trackers and without your browser ever talking to CeX.

> Work in progress. The roadmap lives in the
> [issues](https://github.com/darlopvil/webye/issues).

This repository is the source code of a private instance, built for personal
use. No public instance is offered. Issues are welcome.

## Features

- Search the whole CeX catalogue with the same filter as its website (for
  sale and in stock), 170 results per page.
- Every result shows its price and what CeX pays for it, in cash or in
  store credit.
- Sort results with the same options as the CeX website: relevance,
  popularity, price, name or rating.
- Filter with the same lists as the CeX website: stores, category, price,
  sold-out products and every product attribute it shows (PEGI rating,
  grade, condition…). Filtered CeX links work as they are.
- Product pages with specifications, description, rating and online or
  in-store stock.
- Price data the CeX website does not show: the previous price, the launch
  price and the date of the last price change.
- The same paths as CeX: swap the domain of a CeX link for webye's and you
  land on the same search or product.
- Products without a photo get webye's own placeholder instead of CeX's
  generic picture.
- No JavaScript: plain HTML and CSS.
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
- A `/salud` page for uptime monitors: it reports each CeX service from the
  last real request, makes no request of its own, and turns red when an API
  changes shape. `/salud?destino=algolia`, `wss2` or `imagenes` checks one.

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE).

The bundled fonts keep their own licenses, next to them in `static/fuentes/`:
[Anton](https://github.com/googlefonts/AntonFont) (SIL Open Font License 1.1)
and [Permanent Marker](https://fonts.google.com/specimen/Permanent+Marker)
(Apache License 2.0).

webye is an independent project and is not affiliated with, endorsed by or
connected to CeX (Complete Entertainment Exchange).

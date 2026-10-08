# Runtime country flag atlas

`atlas.v1.json` embeds 252 ISO 3166-1 alpha-2 PNG flags as data URIs. It is
compiled into RenderingGen, so rendering code does not need a network request or
an external image directory.

```go
flag, ok := countryflags.Lookup("IT")
pngBytes, ok := countryflags.PNG("IT")
```

`LookupByName` also resolves English country names and catalog IDs such as
`saudi_arabia`. The runtime motion catalog publishes the matching
`map_flag_country_code` for each country map option.

Source: [FlagCDN](https://flagcdn.com/), 320 px PNG variants.

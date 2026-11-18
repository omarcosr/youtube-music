# YouTube Music

Cliente do YouTube Music para desktop feito com [Tarve](https://tarve.dev): feed inicial, busca, álbuns, playlists e artistas, fila de reprodução, biblioteca após entrar com o Google e letras sincronizadas (LRCLIB).

Uso pessoal: os Termos de Serviço do YouTube não permitem clientes de terceiros.

## Desenvolvimento

```sh
bun install
bun run dev        # recarrega ao editar
bun test
bun run typecheck
```

## Build e instalador

```sh
bun run build      # dist/YouTubeMusic.exe (runtime txiki.js)
bun run package    # dist/release: instalador (setup) e zip portátil
```

Nome, ícone, AppUserModelID (`YouTubeMusic.Desktop`) e opções do instalador ficam no campo `tarve` do `package.json`.

## Downloads

Cada [release](https://github.com/omarcosr/youtube-music/releases/latest) traz um `.exe` portátil por versão:

| Arquivo | App |
| --- | --- |
| `YouTubeMusic-txiki-win32-x64.exe` | TypeScript no txiki.js (o menor) |
| `YouTubeMusic-bun-win32-x64.exe` | TypeScript no Bun |
| `YouTubeMusic-node-win32-x64.exe` | TypeScript no Node.js |
| `YouTubeMusic-go-win32-x64.exe` | Go |

## Atualizações incrementais

Todas as versões se atualizam sozinhas a partir das releases: verificam o manifesto assinado (`latest-<txiki|bun|node|go>.json`) 15 s após abrir e a cada 6 h, baixam em segundo plano e instalam ao sair (ou na hora, pelo toast ou pela notificação); a nova versão roda na próxima abertura.

- **TypeScript:** os executáveis são gerados com `tarve build --packs` e recebem *app packs* (`tarve pack`, ~160 KB): só o código e os arquivos do app, sem o runtime, assinados com Ed25519 e criptografados. Um pack que falha ao iniciar é descartado e o app volta à versão anterior. Quando a release exige um runtime novo, vem o executável inteiro.
- **Go:** o mesmo `latest.json` do `tarve release` e a mesma chave (`go/update.go`); como o host Go não roda packs, vem o executável inteiro, verificado (tamanho, SHA-256) e trocado ao sair. O build de release embute a biblioteca nativa e o ícone.

```sh
bunx tarve keygen             # uma vez (gera ./tarve-update.key: renomeie para youtube-music-update.key; secreta, fora do git)
bun run release               # tudo em dist/github
bun run release:publish       # e cria a release v<version> com o gh
```

- Chave privada: `./youtube-music-update.key` ou `TARVE_UPDATE_PRIVATE_KEY`. A pública fica em `src/youtube-music-updates.ts` e `go/update.go`; o script confere as duas.
- O script precisa de txiki.js (`tjs` ou `TARVE_TXIKI`), Node.js 26.10+, Go e, para a biblioteca nativa do app Go, Rust com o checkout do Tarve em `../tarve` (ou `TARVE_GO_NATIVE` apontando para uma DLL pronta).
- No GitHub, um push da tag `v<version>` (igual ao `package.json`) roda `.github/workflows/release.yml`, que publica a release; ele precisa do secret `TARVE_UPDATE_PRIVATE_KEY`: `gh secret set TARVE_UPDATE_PRIVATE_KEY --repo omarcosr/youtube-music < youtube-music-update.key`.

## Versão em Go

`go/` é o mesmo app sobre o host Go do Tarve, nó a nó. O host Go ainda não tem versão publicada: `go/go.mod` compila contra um checkout do Tarve ao lado deste repositório (`../tarve`).

```sh
cd go
go run .
go test ./...      # inclui a paridade com a versão TypeScript (precisa do bun)
```

O teste de paridade (`go/ytm/parity_test.go`) renderiza os cenários de `go/ytm/testdata/scenarios.json` nas duas versões, com `go/parity/dump.tsx` compilando `src/` pelas fontes do mesmo checkout do Tarve, e exige documentos nativos idênticos.
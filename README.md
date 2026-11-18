# YouTube Music

Cliente do YouTube Music para desktop feito com [Tarve](https://github.com/omarcosr/tarve): feed inicial, busca, álbuns, playlists e artistas, fila de reprodução, biblioteca após entrar com o Google e letras sincronizadas (LRCLIB).

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

Nome, ícone, AppUserModelID (`omarcosr.YouTubeMusic`) e opções do instalador ficam no campo `tarve` do `package.json`.

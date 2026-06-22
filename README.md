# Rift

[Ebitengine Game Jam 2026](https://itch.io/jam/ebitengine-game-jam-2026) 参加作品。テーマは **DISCONNECT**。

ドラッグで「光の刃」を引いて闇を切り、囲んだ領域を一気に光で塗りつぶす。敵は光の縁を侵食して取り戻しに来る。光のカバー率しきい値を制限時間内に超えるとステージクリア。10 ステージ目は巨大ボスを光で封じる。

## 操作

- **ドラッグ** (マウス／タッチ): 光の刃を引く
- 指 / ボタンを離した瞬間に、閉じた暗領域を判定して光で塗る (claim)
- タイトル・リトライ・全クリア画面では **クリック / Space**

## 実行

ネイティブ:

```
make run
```

ブラウザ (WebAssembly):

```
make serve-wasm
# → http://localhost:18081/
```

他のターゲットは `Makefile` 参照。デバッグ表示は `DEBUG=1` または `?debug=1`。

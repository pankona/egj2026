# CLAUDE.md

Ebitengine Game Jam 2026 への参加作品。テーマは **DISCONNECT（切断）**。
コードネームは `Rift`。Go + Ebitengine + WebAssembly。

## ゲーム概要

ドラッグで「方向と起点」を入力すると、指を離した瞬間にドラッグ開始点から方向へ**光の衝撃波**がズバッと伸びる。ドラッグ距離を 3 段階に量子化して 1〜3 ユニットのエネルギーを消費 (短く引けば短い線、大きく引けば長い線)。バッファ容量は 6 ユニットでトリクル回復、3 unit 大ぶり 2 連発も 1 unit 小撃ち連射も両立する。3 連発した直線でポリゴン状に囲んだ暗領域は一気に光で塗りつぶされる（claim）。敵は光の縁を侵食しながら、複数体が連携して囲い返してくる。**ステージの敵を全滅させると次へ**。最終 10 ステージは巨大ボス 1 体を光で封じ込めるとクリア。

- 解像度: 640x480、セル 8px の 80x60 グリッド (`CellSize`, `GridWidth`, `GridHeight`)
- 入力: マウスドラッグ／タッチドラッグ（タッチ優先、`pointerPos` 参照）。フリックでも発動するよう、確定に必要なドラッグ距離は `SlashMinLength` だけ
- 斬撃 (`fireSlash` → `updateSlashes` → `burnSegment` → `illuminate`): ドラッグ開始点を起点に、ドラッグ方向へ可変長 (`ShortLength` / `MidLength` / `LongLength`) で伸びる。長さは `slashSpec` がドラッグ距離を `ShortDragMax` / `MidDragMax` の 2 段で量子化して決め、同時に 1 / 2 / 3 ユニットの energy を消費する。バケット内のドラッグ距離上限は対応する beam length より短く設定してあるので、ビーム先端が指の少し前にはみ出して見える (= 指で線が隠れない)。`SlashRevealFrames` で全長まで展開し、`SlashGlowFrames` で残光が消える。塗り半径は `BladeRadius` セル（`0` で 1 セル幅のヘアライン。包囲した内側 claim を主役にするための極細）、anchored enemy の被弾半径だけ別定数 `SlashHitRadius` セルで太めに保持。`burnSegment` が直前セルとの対角ジャンプを検知すると、両側の orthogonal neighbor 2 セルを補完して 2x2 ブロックを描く。これで 4-連結が切れない (`claimEnclosure` が成立する) のと同時に、ヘアラインでも視覚的に「途切れない太線」に見える (片側 1 セルだけだと L 字キンクが連続して目には「線が切れている」ように映る)。アクティブな斬撃は `Game.slashes []*Slash` で管理し、毎フレーム tip が進んだ分だけ grid に焼く
- 領域確定 (`claimEnclosure`): 斬撃が全長まで伸びた瞬間にプレイフィールド (常時壁の外周 1 セル内側) を 4-連結の暗領域に分解。**画面端に接する成分は「外側」扱いで除外し、完全に閉じた領域だけを claim** する。縦 2 本などの「画面を分割するだけ」のストロークは何も取れず、3 本以上で多角形を組まないと光化されない。内部の敵は claim と同時に除去
- 侵食 (`erodeAround`): 敵のまわりの光セルを暗くする。光の縁 (`WallLightThreshold` 未満の隣接セルを持つセル) は `EnemyEdgeBoost` 倍で削れる。`EffectRadius = 0` の敵（チュートリアル用）は呼ばれても無効 (関数頭で `radius <= 0` を早期 return)。戻り値の `bool` は「実際に光セルを削ったか」で、`true` のとき呼び出し元が `Enemy.Feeding` を立てる
- 捕食スロー (`Enemy.Feeding` × `FeedingSpeedFactor`): 前フレームの `erodeAround` が 1 セル以上削った敵は `Feeding=true` になり、`steerEnemy` 内で `maxSpeed` が `FeedingSpeedFactor` (= 0.5) 倍に落ちる。1 unit 短撃ちを「餌」として置いて敵を釣り、半分速度になった隙に大ぶり 2 連で囲む、というジレンマ駆動の攻略導線を作る
- 結界 (`advanceBindPhase` → `bindEdgesNow` → `bindEnclosure`): `EnableBind` ステージでは全敵共通タイマー `bindPhase` が Roaming → Warning → Holding を周回。Holding 終了フレームで、静止中の敵ペアを暗線として描いた壁集合と既存の暗領域を合算し、明領域を連結成分に分解 → 画面端に接しない閉領域を暗化する。プレイヤー側 claim の鏡写し
- AI (`steerEnemy` / `findNearestLightEdge`): 敵は近傍の光の縁セルを `EnemyRetargetSec` 間隔でロックして寄ってくる。Anchoring 中（`bindPhase != 0`）は移動・侵食・retarget を全部スキップして頂点として固まる

## 全体構成

```
main.go                  Game 本体 (Update/Draw/状態遷移/グリッド・敵・claim)
debug_mode_default.go    DEBUG 環境変数読み取り (非 wasm)
debug_mode_wasm.go       ?debug=1 URL クエリ読み取り (wasm)
devserver/main.go        dist/ を配信する開発用 HTTP サーバ (port 18081)
web/index.html           外側ページ。game.html を iframe で読み込み
web/game.html            wasm 実体を読み込む内側ページ
Makefile                 build / build-wasm / serve-wasm / devserver / fmt / release
```

`Stage` テーブル (`stages`) で `Enemies / EnemySpeed / TimeLimit / Boss / EnableBind / HarmlessEnemy` を 1 行ずつ並べて難易度を作る。新規ステージ追加・調整はここを編集する。`EnableBind` を立てると結界フェーズが解禁され、`HarmlessEnemy` を立てると `EffectRadius=0` の侵食しない敵が生える（ステージ 1 のチュートリアル用）。

ステージ 1〜3 はオンボーディング階段として 1 軸ずつ要素を増やす設計: ステージ 1 = 1 体 / `EnemySpeed=0` / 動かない / 侵食しない (ENCLOSE するだけ)、ステージ 2 = 2 体 / `EnemySpeed=0` / 動かない / **侵食する** (時間プレッシャー初体験)、ステージ 3 = 2 体 / `EnemySpeed=0.40` / **動く** / 侵食する。ステージ 4 で結界 (`EnableBind: true`) 解禁。要素を 1 つずつ足すことで、ゲームオーバーした時に「何が原因だったか」を切り分けて学べる。

`GameState` (`StateTitle / StatePlaying / StateCleared / StateGameOver / StateAllCleared`) の遷移は `Update` のスイッチで一元管理。クリア／ゲームオーバー時の `postClearCooldown` は次入力を受ける前のクッション。勝利条件は全ステージ共通で **`len(g.enemies) == 0`** の一本道。

`Game.grid [GridWidth][GridHeight]Cell` が描画の真実の入れ物。`Cell.Light` が 0..1、`R/G/B` がそのセルに塗られた hue 由来の色。`Draw` 側で `Light * (R/G/B)` を 8bit に量子化して `gridImg` に書き、`CellSize` 倍に拡大して描画。

敵の描画は球面ランバート反射モデル（`Draw` 内の敵ループ）。各敵について全画面のグリッドを線形 falloff で集計し、加重平均から「偏り度 (`asymmetry`)」を計算、`ambient + directional * max(0, n·L)` の合成で `stepPx` 刻みの小矩形格子として塗る。`totalLight == 0` の完全暗闇では描画スキップ。詳細は下の設計メモ参照。

## 操作

| 状態          | 入力                                                                          |
| ------------- | ----------------------------------------------------------------------------- |
| タイトル      | クリック / タップ / Space で開始                                              |
| プレイ中      | ドラッグ。短く引けば短い衝撃波 (1 unit)、中くらいで中 (2 unit)、長く引けば長い (3 unit)。energy が足りない分は自動的に届く範囲まで短縮 |
| クリア        | 自動で次ステージへ                                                            |
| タイムアップ  | クリック / Space で同ステージリトライ                                         |
| 全クリア      | クリック / Space でタイトルへ                                                 |

斬撃は量子化エネルギー制。バッファ容量は `MaxStock` ユニット (現状 6)、ただし 1 ドラッグで消費できるのは `slashSpec` の上限である 1〜3 ユニット。「3 unit 大ぶりを 2 連続でリロード無しに撃てる」「1 unit 小撃ちを 6 連射できる」のバランスでバッファ容量だけ拡張してある。HUD には残ユニットのピップ + (満タンでないとき常時表示される) 次の 1 ユニットまでのトリクルゲージを描画。回復は撃ち切り型ではなく **常時 1 ユニットずつ**: `g.stock < MaxStock` の間は毎フレーム `reloadProgress` が `UnitRecoverFrames` 分の 1 ずつ進み、1.0 に達するごとに 1 ユニット補充。短すぎるドラッグ (`SlashMinLength` 未満) は方向不安定とみなし発射しない。残量より多いユニットを要求するドラッグは「持っている分まで」に自動ダウングレードして発射するので、1 ユニットだけ残っているときの大ぶりも空振りにはならない。ドラッグ中は `slashSpec` が返すユニット数に応じて、ゴースト線の太さ・アルファ・始点リング半径が 1 / 2 / 3 段階で「圧」を増す描画になり、リリース前に「いま離すと何が出るか」が一目で分かる。

ダウングレード発生時は **発射の瞬間に二重の赤フィードバック**を出す: (a) 実際に出たビーム先端から「本来出るはずだった長さ」の末端まで赤いダッシュ 4 本を `UnfiredTailFrames` フレームでフェード (= 不発尾)、(b) HUD で消費された pip 周りに赤いリングが拡大しながら `PipFlashFrames` フレームで消える + 消費 pip 群の背後に赤い帯。サクサク撃つプレイヤーはドラッグ中のゴースト線を見ない (じっくり読む余裕がない) ので、フィードバックはリリース直後 = 視線が必ず画面に戻る瞬間に寄せている。`slashSpec` の戻り値と `bucketedUnitsForDrag` (ドラッグ距離からだけ求めるバケット) の差で「足りなかった分」を判定。

## ビルド・実行

```
make run            ネイティブ実行 (go run .)
make build          ネイティブバイナリを dist/rift に出力
make build-wasm     wasm + wasm_exec.js + web/ を dist/ にまとめる
make devserver      dist/ を localhost:18081 で配信
make serve-wasm     build-wasm → devserver
make watch          air で wasm を再ビルド + devserver を再起動 (初回のみ install-tools)
make release        wasm をビルドして rift-<short-sha>.zip を作る
make lint           GOOS=js GOARCH=wasm go vet ./...
make test           go test -v ./...
make fmt            goimports -w .
```

デバッグ表示 (FPS) は `DebugMode = true` のときだけ。
- ネイティブ: `DEBUG=1 go run .`
- wasm: `http://localhost:18081/?debug=1`

**ネイティブビルド (`go build` / `make build` / `make run`) はこの開発環境では確認しない**。配布形態が wasm 限定で、かつ WSL の開発機には `libXxf86vm` 等の X11 ライブラリが入っていないため、`go build ./...` は毎回リンカエラーで落ちる（環境問題で本件と無関係）。動作検証は常に `GOOS=js GOARCH=wasm go vet ./...`（= `make lint`）と `go test ./...`（= `make test`）で済ませる。

## コーディング規約

- パッケージは `main` 1 つ。ファイルを分けるよりは関数で切る方針。WASM 固有処理だけビルドタグで切り出す (`debug_mode_*.go`)
- 定数は `main.go` 冒頭のブロックに集約。チューニング対象は基本そこに足す
- 命名: グリッド座標は `cx, cy` / セルオフセットは `dx, dy` / ピクセル座標は `x, y` または `px, py` を踏襲
- コメントは「なぜ」だけ書く。`Stage`, `pointerPos`, `claimEnclosure` などにあるブロックコメントの粒度を参考に
- ゲーム挙動を変える前に、まず `stages` テーブルやチューニング定数で再現できないかを確認する

## 設計メモ: なぜ「直線の衝撃波」になったか

初期プロトはフリーハンドのドラッグで軌跡をそのまま光化する素直な作りだったが、テーマ DISCONNECT を体験させる手触りとして「線をなぞる」が弱かった。以下の設計判断を順に重ねて現行の衝撃波方式に着地している。

- **直線一閃**: フリーハンドを捨て、ドラッグの「向き」と「位置」だけを使う。1 操作 = 1 ストロークの儀式にすることで「ズバッと切る」感を出す
- **始点起動・前方展開**: 線の起点 = ドラッグ開始点。ドラッグ方向に量子化された長さで伸びる。中点対称も一度試したが、「指でなぞった範囲とは別の場所に線が出る」「線の中央が指の下に来て見えない」の二重苦で却下。始点起動なら起点 = 過去の指位置（もう指がない）、終点 = 指の少し前（バケット上限を beam length より短くしてある）で線全体が見える。長さは `slashSpec` がドラッグ距離を 3 段量子化して決める (`ShortLength` / `MidLength` / `LongLength`)
- **少操作 = 大影響**: `SlashMinLength` を 12px まで下げ、フリックでも発動する。「ちょっと指を動かしただけで画面に大きな切れ込みが入る」衝撃波感が目的
- **即時展開 + 衝撃波の輪**: `SlashRevealFrames = 1` で全長を 1 フレームで描き、起点 (= ドラッグ開始点) から白い輪を `Frame ≤ 4` の数フレームだけ広げて消す。`SlashGlowFrames` の残光と組み合わせて「ドンッ」のリズム
- **量子化エネルギー (バッファ 6 / 1 ドラッグ最大 3)**: かつての「3 発ストック + 撃ち切りリロード」は単調 (大きな四角を組むだけで一網打尽できる) だったので、1 ドラッグで 1〜3 ユニットを一気消費できる量子化エネルギーに再設計。さらに「3 ユニットだとリロード挟みが多すぎて連射の波が作りづらい」と感じたのでバッファ容量だけ `MaxStock = 6` に拡張し、「3 unit 大ぶり 2 連発」「1 unit 小撃ち 6 連射」が成立するようにした。回復は **常時 1 ユニットずつトリクル** (`UnitRecoverFrames`) なので、撃ち切り後の長い沈黙が消え、攻めながらリズムを刻める。歴史的には初期がフリーハンド + 連続エネルギー (囲むのが簡単すぎた)、次が直線 + 連続エネルギー (連射しすぎた)、次が直線 + 3 発撃ち切りストック (大箱で一網打尽できた)、次が直線 + 量子化エネルギー 3 unit (リロード待ちが間延びした) を経て現行 6 unit バッファに着地
- **claim は完全閉領域だけ**: 当初は「画面端から flood-fill して外側を確定 → 残りを claim」だったが、画面端を「常時壁」にしたら起点エリア = 内側全域につながって claim できなくなった。次に「暗領域を連結成分に分解 → 最大成分を外側 → 残りを claim」に変えたが、これだと縦 2 本引いただけで両端の細長い 2 領域が claim されて 50% 以上塗れてしまうチート気味のムーブが成立した。現行は **「画面端の内周 1 セルに接する成分は『外側』扱いで除外、画面端と縁を共有しない完全閉領域だけ claim する」**。直線 3 本で三角形を組む儀式が claim 成立の最小単位になっている
- **画面端は常時の壁**: 最外周 1 セルを「あらかじめ引いてある線」として扱い、Draw 側で 1 セル幅のフレームを描画。`claimEnclosure` の連結成分判定は内周 1 セルに接した時点で「外側」と判定する。画面端と斬撃を組み合わせて細長い領域を切り出しても、それは画面端に接しているので claim 対象外
- **ドラッグ予告は三層 + unit ステップ表示**: ドラッグ中は (a) 始点〜現在ポインタの短い実線 (指先に追従する手触り)、(b) 始点から発射方向に length 分伸びる薄いゴースト線、(c) 始点に置かれた予告リング — を重ねて描画する。(b)(c) の線幅・アルファ・リング半径は `slashSpec` が返す unit 数 (1/2/3) で 3 段階に「圧」を増し、リリース前に「いま離すと何が出るか」が一目で分かる
- **線は細く、包囲が主役**: 当初 `BladeRadius = 2` (幅 32px) は斬撃 1 本で画面の 3.3% を即点灯させ、3 本引くだけで線の面積だけで約 10% が光化していた。これだと「囲って中身を取る」リワード感が薄れ、包囲を成立させずにライト % を稼ぐ立ち回りが許容されてしまう。`BladeRadius = 0`（中心 1 セル = 8px 幅のヘアライン）まで下げて「線そのものは細く、囲った中身が大きな報酬」という構図に体験を寄せた。素朴に半径 0 にすると斜め斬撃で 1px サンプリングが (a,a)→(a+1,a+1) と対角ジャンプし 4-連結が切れて claim が成立しない罠があるので、`burnSegment` 側で前回セルと比較して対角ジャンプを検知 → 両側の orthogonal neighbor 2 セルを塗って 2x2 ブロックにする。当初は片側 1 セルだけ補完していたが、それだと L 字キンクが連続してプレイヤーには「線が途切れている」と見えてしまったため、両側補完に切り替えた (claim 領域への影響は内側 1 セル程度で無視できる)。被弾判定だけ `SlashHitRadius` セルで太く保ち、結界の頂点を斬る手応えは維持

将来「画面が物理的に裂ける」「闇のかけらが破片化して落ちる」などの別アイデアは別軸として保留中。まずはこの衝撃波体験の調整 (`ShortLength` / `MidLength` / `LongLength` の長さ、`UnitRecoverFrames` の回復テンポ、`ShortDragMax` / `MidDragMax` の量子化しきい値、ステージごとのユニット数) を優先する。

## 設計メモ: なぜ敵に「結界」を持たせたか

初期実装の敵は `erodeAround` で光の縁をジワジワ削るだけで、プレイヤーへの直接の脅威がなく、無視して claim を回しても勝てる消化試合になりがちだった。テーマ DISCONNECT の「光と闇のせめぎ合い」を儀式の対称として見せるため、敵側にもプレイヤー claim と鏡写しになる「結界」儀式を持たせている。

- **3 段サイクル**: `bindPhase` が Roaming (`0`) → Warning (`1`、脈動オーラの予告) → Holding (`2`、暗線が中点から両端へ伸びるアニメ) を周回。タイマーは全敵共通で、`BindRoamFrames` / `BindWarnFrames` / `BindHoldFrames` で各フェーズの長さを定義
- **頂点と暗線**: Anchoring 中（phase 1/2）は敵が一斉に静止して頂点になる。`bindEdgesNow` が距離 `BindRangeCells` 以内のペアを動的に列挙、`severedPairs` に登録されていない組だけが暗線として描画・暗化に寄与する
- **介入手段**: 静止中の敵に斬撃が当たれば撃破（`fireSlash` 内で `pointSegmentDistance` 判定、撃破した頂点を含むペアは自動で無効化）。完成中の暗線と斬撃の線分が `segmentsIntersect` で交差すれば、その組が `severedPairs` に登録されて bindEnclosure の壁ラスタライズから除外される
- **bindEnclosure**: claim の鏡写し。暗線をラスタライズした dark cells に既存の暗領域を足して壁集合とし、「明領域」を 4-連結成分に分解、`claimEnclosure` と同じく画面端に接しない閉領域だけを `Light=0` に落とす
- **Boss と敵 1 体ステージはスキップ**: 結界は最小 2 頂点が要るので、`advanceBindPhase` は `s.Boss || len(g.enemies) < 2` で phase 進行を止める。`severedPairs` も同時にクリア

## 設計メモ: なぜ勝利条件を「敵全滅」に揃えたか

初期は「光カバー率しきい値 (`WinThreshold`) を制限時間内に達成」が勝利条件だったが、ボスステージだけ別ロジック (`len(g.enemies) == 0`) が走っていて分岐が二重に走っていた。プレイヤー視点でも「カバー率を満たして勝ちが確定したのに、敵がまだ画面に残っている」状態が起きて、消化試合のような違和感が出ていた。

判定を一本化して「敵全滅でクリア」に統一。`Stage.WinThreshold` フィールドも削除。ステージ 1 は敵 0 では成立しないので、`HarmlessEnemy: true` の **完全静止** (`EnemySpeed=0`)・`EffectRadius=0`（侵食しない）の 1 体に置き換え、「斬撃を引いて閉領域で claim する」基本ループを 30 秒で学ばせるチュートリアルに。`loadStage` 内でステージ 1 だけ敵を画面中央 (320, 240) 固定スポーンし、`fireSlash` での最初の発射時に `repositionTutorialFoeAwayFrom` が呼ばれてビーム中点から **垂直方向に 90px オフセットした位置にテレポート**する（最初のスラッシュ直後はまだ light が広がっていないので、移動はプレイヤーから視認されない）。これで「最初の 1 本が敵を貫いて見えなくなる」事故が構造的に消える。光カバー率は HUD の進捗メータ（`Light xx%`）として残してあるが、勝敗には関わらない。

敵を倒す手段は (a) `claimEnclosure` で完全閉領域を作って中の敵を除去、(b) 結界の Anchoring 中に斬撃で頂点を撃破、の 2 ルート。Roaming 中の敵は斬撃で倒せないため、結界フェーズが「攻めのチャンス」になるリズムを意図的に作っている。

## 設計メモ: なぜオンボーディングと文字描画をこう作ったか

ゲームジャム提出版でテストプレイヤーから「開始直後に何をしたらいいか分からない」という声が複数挙がっていた。原因を分解すると、(1) タイトル画面の英文 2 行は読み飛ばされやすい、(2) 開始時のステージ 1 は完全暗闇で `totalLight=0` の敵が描画スキップされ「画面に何もない」状態になる、(3) HUD と全状態遷移メッセージが `ebitenutil.DebugPrintAt` の固定幅 ASCII ビットマップで読みにくい、の 3 つだった。以下の判断で対応している。

- **`text/v2` + 同梱フォント**: 文字描画を `github.com/hajimehoshi/ebiten/v2/text/v2` + `examples/resources/fonts.MPlus1pRegular_ttf` に差し替え。外部アセットを足さずに済む (`go mod tidy` で `golang.org/x/image` 系が間接依存に入るだけ)。3 サイズ (`faceLarge` 32 / `faceMid` 16 / `faceSmall` 12) を `Game` に保持し、`drawCenter` / `drawCenterFace` / `drawAt` の 3 ヘルパーで使い分け。**`text.DrawOptions.ColorScale.ScaleWithColor` は色を premultiplied alpha として扱う** ため `color.RGBA{R, G, B, A<255}` を渡すとグリフが滲んで隣文字と被って見える。フェードする SEALED! やデフォルト dim 色は必ず `color.NRGBA` で渡すこと
- **タイトル画面は 3 行に圧縮**: 旧版の長文 2 行 (`Drag a straight slash. ...` / `Encircle the dark...`) を捨て、`RIFT` / `DRAG. SLASH. ENCLOSE. SEAL.` / `Click / Space` の 3 行に。4 拍の動詞リズムでゲームの動詞を宣言する形にして、読まなくても語感で意味が伝わるようにしている (国際的なジャムなので日本語化はせず、英単語を最小限に絞る方針)
- **ステージ 1 インラインチュートリアル**: 画面下中央に単語 1 個のヒントを表示。進行を **ドラッグ回数ではなく game event でガード**: ステップ 0 = `DRAG` / 開始時 → 最初の `fireSlash` 成功で 1 へ、ステップ 1 = `ENCLOSE` / claim 成立 (`claimEnclosure` が敵を 1 体以上消した) で 2 へ。なぜ stroke 数ベースにしないか: 3 本引いても閉領域が画面端に接していたり線が交差していなかったりで claim 不成立になる場合があり、「`1 MORE` の次で何も起きない」状態になると詰む。event ベースなら、囲めるまで `ENCLOSE` が残り続けて誘導が破綻しない
- **ステージ 1 の敵を最初のスラッシュの真横にテレポート**: 上の「設計メモ: なぜ勝利条件を全滅に揃えたか」参照。`repositionTutorialFoeAwayFrom` がビームの perpendicular に 90px (片側で安全マージン 60px 内に収まる方を選ぶ、両方 OK なら画面中央に近い方) 移動させる。最初のスラッシュ後の light が広がる前 = 同一フレーム内で実行するので、テレポート自体は見えない
- **HUD は数字に語らせる**: ラベル全削除、`STAGE 1 / 10` → `1 / 10` (左上 `faceMid`)、`FOES 3` → `3` (中央 `faceLarge`、勝利条件の数字を最大強調)、`30.0s` (右上 `faceMid`)、`35%` (左下 `faceSmall`、補助情報)。`Foes` 数字を最大にすることで「これを 0 にすれば勝ち」が一目で伝わる
- **`SEALED!` フラッシュ + StatePlaying ゲート**: `claimEnclosure` が敵を 1 体以上消したフレームで `sealedFlashFrames = 30` をセット、`Update` 内で減算してフェードさせる。**`g.state == StatePlaying` でガード必須**: 最後の 1 体を倒した瞬間は同フレーム内で sealedFlashFrames セット + `len(enemies)==0` 判定 → `StateCleared` 遷移が起きる。次フレーム以降 `updatePlaying` が呼ばれないので sealedFlashFrames は固定値のまま残り、StateCleared の CLEAR メッセージと同じ y 座標 (`ScreenHeight/2-20`) に重ね描きされて「複数の文字が重なって見える」事故になる。StatePlaying ガードで「クリア時は CLEAR だけ」「中盤で敵を封じた時だけ SEALED!」が自然に成立する

## 設計メモ: なぜ敵を球面ランバートで描くか

斬撃を引いてないステージ序盤は画面のほとんどが暗闇で、敵の位置が当てずっぽうになる課題があった。プレイヤーフィードバックでは「斬撃 = 光源」「敵の輪郭が光に当たって浮かび上がる」のメタファーがハマっていたので、敵の描画を「斬撃で塗られた光を集めて球体に当てるランバート反射」モデルに書き直した。Kage シェーダーは使っていない — 全部 CPU 計算で `vector.DrawFilledRect` を格子状に並べる方式。

- **ライト集計**: 敵ごとに `lightMaxDist`（= 100 セル、画面対角を覆う）範囲内のグリッドを全走査。線形 falloff (`1 - d/maxDist`) で `totalLight` / 加重方向ベクトル (`dirX`, `dirY`) / 加重色 (`litR/G/B`) / 加重距離 (`totalDistance`) / 最大単一寄与 (`maxContribution`) を集計
- **brightness の根拠**: `sqrt(total/N)` や正規化ではなく `maxContribution` を使う。これで「画面のどこかに光があれば必ず brightness > 0」が保証され、敵から離れた初撃でも敵が薄く浮かぶ
- **ambient と directional の分離**: 加重平均距離 `avgD = totalDistance / totalLight` で `asymmetry = dirLen / (totalLight * avgD)` を 0..1 で計算。**単一光源 → asymmetry ≈ 1**（片側だけ照らされる Lambertian）、**四方光源 → asymmetry ≈ 0**（全周一様の ambient）。`ambient = brightness * (1-asymmetry) * 0.85`、`directional = brightness * asymmetry`、`intensity = ambient + directional * max(0, n·L)`
- **球面サンプリング**: 敵を `stepPx` 刻みの矩形格子で塗る（通常敵 1.5px、ボス 3.0px）。各点に法線 `n = (sx/r, sy/r, sqrt(r² - sx² - sy²)/r)` を与え、光源ベクトルは画面平面 + `lz = 0.5` の 3D ベクトルに正規化。`lz` を浮かせるとハイライトが球面の内側に乗り、輪郭ではなく中心寄りが最も明るくなる
- **完全暗闇は描かない**: `totalLight <= 0` なら敵描画スキップ。ステージ 1 で「最初の斬撃を引いて初めて敵が見える」探索体験を成立させる土台

## 設計メモ: なぜ「捕食スロー」と「不発尾フィードバック」を入れたか

子供 (≒ 初見プレイヤー) のプレイテストで 2 つの課題が見えた: (1) 「敵を線で囲んで倒す」がそもそも伝わらないまま無謀に長い線を引き続け、(2) エネルギーが足りないのに大ぶりを連発してジリ貧、イライラする。ジレンマとして設計したものだが、初見では「理不尽」と感じられて学習が止まる。ベース機構の learnability を上げつつジレンマ自体は残す方向で 2 つ追加した。

- **捕食スロー (`Enemy.Feeding`, `FeedingSpeedFactor = 0.5`)**: 敵は光の縁を侵食しに来るが、実際に削っている間は移動速度が半分になる。「1 unit 短撃ちを餌として置く → 敵がそこに群がって減速 → 大ぶり 2 連発で囲んでまとめて claim」という囮戦術が成立する。実装は `erodeAround` の戻り値で「実際に削ったか」を判定し、その敵を `Feeding=true` に立てて `steerEnemy` の `maxSpeed` を半減するだけ。視覚的な「スローエフェクト」は別途付けていない — 敵が侵食中なら定義上その敵は光に乗っている = Lambertian で自然に明るく見える、という既存表現を信用している
- **不発尾 (`Slash.HasUnfired` + 赤ダッシュ)**: 子供は線を「シャッシャッ」と高速に引くため、ドラッグ中のゴースト線 (本来は事前の駆け引き用) を見る暇がない。よってフィードバックはリリース直後 = 視線が必ず画面に戻る瞬間に寄せる。`bucketedUnitsForDrag` がドラッグ距離からだけ求めた要求 unit と、`slashSpec` の実際 unit との差分を取って、本来出るはずだったビーム末端を赤いダッシュ 4 本でチカッと光らせる。`UnfiredTailFrames = 8` フレームで消える短さ
- **HUD ピップ強フラッシュ (`pipFlash*`)**: 不発尾と対の存在。消費された pip スロットに `PipFlashFrames = 16` フレームかけて赤いリングが拡大しながら消える + 背後に赤い帯。「いま何 unit 使った」が周辺視野で読める。ピップは小さいので不発尾と帯フラッシュをセットにして二重に冗長化している

注意点として、`erodeAround` を `bool` 返しに変えるついでに `radius <= 0` で早期 return する分岐を入れた。CLAUDE.md にはもともと「`EffectRadius = 0` の敵は呼ばれても無効」と書いてあったが、実装は中心セル + 4 近傍を rcell=1 で薄く削っていたのを発見 (= ステージ 1 のチュートリアル敵がほぼ無視できる速度で claim を削っていた)。早期 return で挙動を doc に合わせた。

`FeedingSpeedFactor` を 0.5 にしたのは「囮戦術が確かに機能する」のを子供にも体感させるため。0.7 にすると減速がうっすら過ぎて「あれ？」となり、学習として成立しない。ジレンマを残すなら侵食速度を上げる別の手があるが、まずは「簡単すぎるくらいでいい」基準で 0.5 を採用。難しすぎなら 0.6〜0.7 に締める。

## ジャム制約

- 期間内に作ったものだけが対象。前年作品 (`pankona/egj2025`) からのコピーは禁止。`Makefile` / wasm 周りの定型部分は **参考にして書き直す** 方針
- 1 週間（〜2026-06-28 前後）の 1 人開発。残り時間が短いので、まず最小プロト → 手触り → ステージ拡張の順を崩さない
- 動詞「切る／囲んで光を奪い返す」がテーマ DISCONNECT を体験させる軸。「ただ避けるアクション」に薄まらない調整を優先する

## 現在のチューニング定数 (引き継ぎ用一覧)

```
// 斬撃のジオメトリ
BladeRadius        = 0     // セル幅。0 = 1 セルヘアライン。burnSegment が対角ジャンプ補完
SlashHitRadius     = 2     // セル幅。anchored enemy 被弾半径 (細線でも当てやすく)

// エネルギー (バッファ + 量子化 + トリクル回復)
MaxStock           = 6     // バッファ容量 (ユニット)。1 ドラッグの上限は依然 slashSpec の 3
UnitRecoverFrames  = 60    // 1 ユニット回復に要するフレーム (1s @ 60fps)
SlashMinLength     = 12    // ドラッグ最小しきい値 (px)
ShortDragMax       = 100   // この距離以下 = 1 unit バケット
MidDragMax         = 200   // この距離以下 = 2 unit、超 = 3 unit
ShortLength        = 130   // 1 unit の線長 (px)。ShortDragMax より大きく取って指の隠れを回避
MidLength          = 240   // 2 unit の線長 (px)。MidDragMax より大きく
LongLength         = 360   // 3 unit の線長 (px)。drag 200〜360 までは指の前に tip がはみ出る

// 演出タイマー
SlashRevealFrames  = 1     // 全長まで展開するフレーム数 (1 = 即時)
SlashGlowFrames    = 14    // 残光フレーム数

// 不足フィードバック (リリース後の赤フラッシュ)
UnfiredTailFrames  = 8     // 「足りなかった分」の赤ダッシュ表示フレーム
PipFlashFrames     = 16    // 消費 pip 周りの赤リング表示フレーム

// 敵の捕食スロー
FeedingSpeedFactor = 0.5   // 削ってる間の速度倍率。0.5 = 半分。Feeding は erodeAround の戻り値で自動切替
```

線長 > その unit のドラッグ上限 は「指で線が隠れない」の前提。`ShortDragMax / MidDragMax` を上げる場合は対応する `Length` も上げないと指隠れ問題が再発する。

## 引き継ぎ: 未確認のバランス課題

2026-06-27 の調整セッション (`3a6f460` ヘアライン化 + `ffd0e52` 量子化エネルギー & 始点起動) 直後の状態で残っている観察ポイント:

- **後半ステージの難易度**: 6 unit バッファ + 連射の波で「ついにクリアできないステージが出てきた」状態。捕食スローで一段緩和されたはずだが、依然キツければ `EnemySpeed` / `BindHoldFrames` / `EnemyEdgeBoost` で調整。`MaxStock` を 4〜5 に戻す手もある
- **捕食スローのバランス**: `FeedingSpeedFactor = 0.5` は「簡単寄り」スタート設定。プレイ感が緩すぎる (「囮置いとくだけで詰む敵が出ない」) ようなら 0.6〜0.7 に締める。逆に効果が薄ければ侵食速度 (`EnemyEdgeBoost`) を上げる方向で「速いけど痛い vs 遅いけど穏やか」のトレードオフを足す手がある
- **敵に種類を持たせる拡張余地**: 「移動速い × 食うの遅い」(忍者) と「移動遅い × 食うの速い」(ブルドーザー) のように `Speed` と `EffectRadius` (もしくは別の `ErodeMultiplier` を新設) を独立に振る案を保留中。今は learnability 優先で 1 種類のまま
- **ステージ別の `MaxStock`**: 現状は全ステージ共通 6。チュートリアル (ステージ 1) は 3〜4 に絞って学習負荷を下げる、ボス (ステージ 10) は 8 に増やす、などの拡張余地あり。`Stage` 構造体への昇格を検討
- **量子化のヒステリシス**: 1 unit と 2 unit の境界 (100px) で意図せず 2 unit になる例があれば 90px に下げる、または「ドラッグ距離が短くなる方向に動かしている間はバケット降格しない」ヒステリシスを入れる
- **回復ペース**: フル消費 → フル充填 6 秒が長すぎたら `UnitRecoverFrames = 45` に。逆に「常に満タン気味で攻め放題」なら 75〜90 に締める
- **6 ピップ HUD の密度**: `pipGap = 16` のままで 6 個並ぶと x=14〜94。Stage 行 (y=24) とは被らないが、横長になる。詰めたい場合は `pipGap` を 12〜14 に
- **新しい線長で claim 成立する最小ポリゴン**: `ShortLength = 130` の三角形は内側面積がかなり小さい。「小撃ち 3 連射で素早く小さい三角を取る」が機能するかは要確認
- **ステージ 2 のランダム配置と最初のスラッシュ被り**: ステージ 2 は 2 体ランダム配置で `repositionTutorialFoeAwayFrom` 対象外。最初のドラッグが敵を貫いて見えにくくなる事故が起きる可能性あり。気になるなら `g.currentStage == 0` のガードを `<= 1` に広げて 2 体それぞれを近傍候補に再配置する手がある

# 概要

petsnowy-cliはPetSnowyアプリ(Tuya Smart SDK 7.5.0ベース)のクラウドAPI(https://a1.tuyaus.com/api.json)を直接呼び出すGo製CLIツール。TUIとRESTful Web APIサーバの機能を持つ

# セットアップ

ビルド: `go build -o petsnowy-cli .`(Go 1.26、外部依存はgo-tuiとgolang.org/x/term)

初回ログイン: `./petsnowy-cli auth login -email EMAIL -password PASSWORD`(引数なしで対話型入力、パスワードはエコーバックなし)

セッションは`~/.petsnowy-cli/session.json`に保存され、以降のコマンドはログイン不要

`auth status`でセッション状態確認、`auth logout`でセッション削除

# CLIコマンドリファレンス

`time` — サーバ時刻取得(ログイン不要)

`devices` — デバイス一覧(dpCode解決済み)

`state --name NAME | --id ID` — デバイスのdp状態表示

`model --name NAME | --id ID` — dpCode/dpID対応表(thing model)

`op --name NAME <dpCode> <value>` — dpCode指定で操作

`clean --name NAME` — 猫トイレ手動清掃

`feed --name NAME [n]` — 給餌器の手動給餌(デフォルト1)

`light --name NAME on|off` — ライト制御

`sleep --name NAME on|off` — スリープモード

`send --id ID <dpsJson>` — 生dp送信(thing.m.device.dp.publish)

`serve [-addr :8080] [-token T]` — RESTful Web APIサーバ起動

グローバルフラグ: `-email`, `-password`, `-countryCode`(デフォルト1), `-force`, `-format text|json`(デフォルトtext)

`-name`/`-id`/`-format`フラグはコマンドライン中のどこに書いても有効

# 出力フォーマット

対象: `devices`, `state`, `model`, `op`, `clean`, `feed`, `light`, `sleep`, `send`

`state --format json`の出力はdevView構造: ID, Name, Online, Values(dpCodeごとの現在値), Status(読み取り専用dp), Props(書き込み可能dpの仕様)

op系のJSON出力例:
```json
{"ok": true, "code": "light", "value": true, "response": "true"}
```

# REST APIリファレンス

起動: `./petsnowy-cli serve -addr :8080 -token SECRET`(-token省略可。省略時は認証なし、指定時は`Authorization: Bearer <token>`ヘッダー必須)

`GET /api/v1/health` — ヘルスチェック(認証不要)。レスポンス: `{"status": "ok", "time": "<RFC3339>"}`

`GET /api/v1/homes` — ホーム一覧

`GET /api/v1/devices` — デバイス一覧(devView配列)

`GET /api/v1/devices/{id}` — デバイスのdp状態

`GET /api/v1/devices/{id}/model` — thing model

`POST /api/v1/devices/{id}/op` — dpCode指定操作。ボディ: `{"code": "light", "value": true}`。危険操作は`{"code": "empty", "value": true, "force": true}`のように`force`必須

`POST /api/v1/devices/{id}/dps` — 生dp送信。ボディ: `{"16": true}`

エラー時: 401(未認証/セッション無効), 400(危険操作ガード、ボディ不正、APIエラー), 404(デバイス未検出), 502(クラウドAPIエラー)

# TUIモード

引数なし起動でTUI。デバイス一覧、Status(読み取り専用dp)、Controls(書き込み可能dp)、コマンド入力ログ

TTY非対応環境では簡易REPLへフォールバック

# エラーハンドリング

クラウドAPIのレート制限(`REQUEST_TOO_FREQUENTLY`)と`PERMISSION_DENIED`は12秒待って1回自動リトライ

ログインは10秒以上間隔を空けること

署名キーはアプリバージョン7.5.0のAPKから抽出した値であり、アプリ更新で失効する可能性

危険操作ガード対象dpCode: `empty`(砂捨て), `factory_reset`, `ball_reset`, `filter_reset`, `desiccant_reset`

CLIでは`-force`フラグ、REST APIではボディの`force: true`が必要

`send`コマンドと`POST /dps`は生dp送信のため値の意味を確認してから実行すること

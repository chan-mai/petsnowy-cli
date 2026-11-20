# petsnowy-cli

petsnowy-cliは、PetSnowyアプリ(Tuya Smart SDK 7.5.0ベース)のクラウドAPIをアプリ外から直接呼び出すためのGo製CLIツールです。Tuyaアカウントのemailとパスワードを用いてログインし、デバイス一覧の取得、dp状態の参照、dp送信によるデバイス操作が可能です。

## 免責事項

本ツールは学習・研究用途に限定して提供されます。PetSnowyおよびTuyaの公式ツールではなく、公式APIの保証対象外です。アプリのアップデートやAPIの仕様変更により、動作しなくなる可能性があります。本ツールの使用によって生じたいかなる損害(デバイスの誤動作、データ損失等を含む)についても、作成者は一切の責任を負いません。

## ビルド

外部依存は`golang.org/x/term`のみです。以下のコマンドでビルドできます。

```bash
go build -o petsnowy-cli .
```

## 使い方

初回は`auth login`でログインし、セッションを保存します。デバイスは表示名(`--name`)またはID(`--id`)で指定できます。`devices`/`state`/`model`/`op`系コマンドは`-format json`を付与することでJSON形式で出力します(デフォルトはtext)。危険な操作(`empty`, `factory_reset`, `ball_reset`, `filter_reset`, `desiccant_reset`)は`-force`フラグが必要です。

## コマンド一覧

```
./petsnowy-cli auth login [-email EMAIL -password PASS]  # ログイン&セッション保存(引数なしで対話型入力、パスワードはマスク)
./petsnowy-cli auth status                               # セッション状態確認
./petsnowy-cli auth logout                               # ログアウト(セッション削除)
./petsnowy-cli time                                      # サーバ時刻取得(ログイン不要)
./petsnowy-cli devices                                   # デバイス一覧
./petsnowy-cli state --name NAME | --id ID               # dp状態表示
./petsnowy-cli model --name NAME | --id ID               # dpCode/dpID対応表
./petsnowy-cli op --name NAME <dpCode> <value>           # dpCode指定で操作
./petsnowy-cli clean --name NAME                         # 手動清掃
./petsnowy-cli feed --name NAME [n]                      # 手動給餌
./petsnowy-cli light --name NAME on|off                  # ライト
./petsnowy-cli sleep --name NAME on|off                 # スリープモード
./petsnowy-cli send --id ID <dpsJson>                    # 生dp送信
```

## 詳細

本ツールはAPKの静的解析と通信キャプチャ, メモリダンプ取得等の動的解析により、固有パラメータを抽出しています。リクエストはHMAC-SHA256署名と`et=0.0.1`(平文モード)で送信されます。ログインフローでは、`smartlife.m.user.username.token.get`(v2.0)でRSA公開鍵とtokenを取得し、パスワードのMD5ハッシュをRSA-PKCS1v15で暗号化、`smartlife.m.user.email.password.login`(v3.0)に`ifencrypt:1`付きで送信し`sid`を取得します。セッションは`~/.petsnowy-cli/session.json`に保存されます。起動時に全デバイスのthing modelを自動取得し、dpCodeとdpIDの対応を解決してます。署名キーはアプリバージョン7.5.0のAPKから抽出した値であり、今後の更新により失効する可能性があります。


[![技術者倫理 遵守済み](https://img.shields.io/badge/%E6%8A%80%E8%A1%93%E8%80%85%E5%80%AB%E7%90%86-%E9%81%B5%E5%AE%88%E6%B8%88%E3%81%BF-0a0a0a?style=for-the-badge&labelColor=ffffff)](https://技術者倫理.com)

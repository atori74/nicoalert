# nicoalert

## What is this

ニコニコからのプッシュ通知を受取って標準出力にjson形式で出力します。  
受け取れる内容はニコニコのブラウザプッシュ通知機能と同等ですが、ブラウザを立ち上げる必要なくシングルバイナリで動作します。  

他のプロセスへパイプするなどして、生放送の自動録画のトリガー等に使うことができます。

## How to use

### 必要な環境変数

|                 |                                  |
|-----------------|----------------------------------|
|NICONICO_EMAIL   |ニコニコアカウントのメールアドレス|
|NICONICO_PASSWORD|ニコニコアカウントのパスワード    |

### 実行

実行コマンド
```sh
$ ./nicoalert
```

出力例 (実際は1件あたり1行のjsonで出力されます)
```
{
  "body": "[コミュニティ名] で、「[生放送タイトル]」を放送",
  "data": {
    "created_at": "2024-02-13T11:00:00Z",
    "on_click": "https://live.nicovideo.jp/watch/[生放送ID]?from=webpush&_topic=live_user_program_onairs",
    "tracking_parameter": "live_onair-[生放送ID]-webpush-nico_account_webpush",
    "ttl": 600
  },
  "icon": "https://secure-dcdn.cdn.nimg.jp/nicoaccount/usericon/...",
  "title": "[ユーザー名]さんが生放送を開始"
}
```

## How it works

Webpushの仕組みを利用しており、ブラウザのServiceWorker(User Agent)として振る舞います。  
Push Serviceとして[Mozilla Push Service](https://mozilla-push-service.readthedocs.io/en/latest/)を利用しています。  

### 参考にさせていただいたドキュメント

nicoLiveCheckTool/push.md at master · guest-nico/nicoLiveCheckTool  
https://github.com/guest-nico/nicoLiveCheckTool/blob/master/push.md

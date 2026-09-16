# dekapu-master-detector

VRChatのログを監視して、自分がインスタンスマスターかどうかを判定し、アバターパラメータとしてOSC送信するツール。

## 動作概要

- `%USERPROFILE%\AppData\LocalLow\VRChat\VRChat\output_log_*.txt` のうちファイル名が辞書順で最新の1本を60秒間隔で選び直す。VRChat再起動で新しいログファイルができれば自動で乗り換える
- 読み取り位置は保存しない。起動やファイル乗り換えのたびに先頭から全量リプレイし、末尾に追いついてから(LIVE)結果を外部へ出す。リプレイ中の状態変化はOSCに流れない
- 判定はログ内のPersistence復元行とSanity check行の追随関係を使う。自分がマスターの間だけ復元行の直後にSanityが続く、という実ログで確認した法則が根拠。詳細は internal/detector のコメントを参照
- 判定の時間処理はログのtimestamp基準なので、リプレイ時に実際の経過時間の影響を受けない
- ログファイルの更新が15分止まったらVRChat終了とみなし、状態をUNKNOWN(=false)に落とす

## OSC出力

`127.0.0.1:9000` の `/avatar/parameters/MasterDisplay/IsMaster` へboolを送る。変化時に加えて3秒間隔で再送する(アバター変更でパラメータが揮発するため)。送信先やパラメータ名を変えるときは main.go の定数を書き換える。

判定パラメータ(バーストマスク120秒、Sanity追随窓10秒、奪取確定3連続)は internal/detector/detector.go の定数にある。

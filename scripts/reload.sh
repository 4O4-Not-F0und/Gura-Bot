#!/bin/sh

echo "[*] Sending SIGHUP"
/usr/bin/killall -s HUP telegram_translate_bot
echo "[*] Sent SIGHUP, exit code $?"

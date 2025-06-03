#!/bin/sh

echo "[*] Sending SIGHUP"
/usr/bin/killall -s HUP gura_bot
echo "[*] Sent SIGHUP, exit code $?"

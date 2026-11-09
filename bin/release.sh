#!/bin/sh
set -eu

PROJECT_NAME="senspace"
PROJECT_PATH="/usr/local/app/$PROJECT_NAME"

BASEDIR=$1
SERVICE=$2
ENV=${3:-uat}

echo "开始部署 $ENV 环境"

if [ -d "$BASEDIR/deploy" ]; then
  WORKSPACE_DIR=$BASEDIR
elif [ -d "$BASEDIR/../deploy" ]; then
  WORKSPACE_DIR=$(CDPATH= cd -- "$BASEDIR/.." && pwd)
else
  echo "未找到 deploy 目录，请从工作区根目录或后端目录执行"
  exit 1
fi

cd "$WORKSPACE_DIR"
sh deploy/scripts/prepare-terrain-catalog.sh

echo "开始复制项目到服务器 $SERVICE"
ssh root@"$SERVICE" "mkdir -p $PROJECT_PATH /var/log/$PROJECT_NAME"
scp -r \
  deploy \
  senspace \
  senspace-web \
  root@"$SERVICE":"$PROJECT_PATH"

ssh root@"$SERVICE" "cd $PROJECT_PATH/deploy && sh scripts/start.sh"

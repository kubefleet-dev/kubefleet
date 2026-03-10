import os, sys
from azure.identity import DefaultAzureCredential
from azure.storage.queue import QueueServiceClient, QueueClient, QueueMessage, BinaryBase64DecodePolicy, BinaryBase64EncodePolicy

STORAGE_ACCOUNT_NAME = os.getenv("STORAGE_ACCOUNT_NAME")
if len(STORAGE_ACCOUNT_NAME) == 0:
    raise Exception("Missing environment variable: STORAGE_ACCOUNT_NAME")

QUEUE_NAME = os.getenv("QUEUE_NAME")
if len(QUEUE_NAME) == 0:
    raise Exception("Missing environment variable: QUEUE_NAME")

args = sys.argv[1:]

account_url = f"https://{STORAGE_ACCOUNT_NAME}.queue.core.windows.net"
default_cred = DefaultAzureCredential()
queue_client = QueueClient(account_url=account_url,
                           queue_name=QUEUE_NAME,
                           credential=default_cred)

for i in range(201, 500):
    queue_client.send_message(f"cluster-{i}")

FROM python

RUN apt-get update
RUN apt-get install build-essential -y

RUN pip install --no-cache-dir azure-cli

ADD requirements.txt .
ADD retrieve_from_queue.py .
RUN pip install -r requirements.txt

ADD run.sh .
RUN chmod +x run.sh

ENV RESOURCE_GROUP_NAME="dp-perf-test"
ENV LOCATION="westus2"
ENV REGISTRY_NAME="fleetperftest"
ENV STORAGE_ACCOUNT_NAME="fleetbuilder"
ENV QUEUE_NAME="clusterstoadd"

CMD ["./run.sh"]

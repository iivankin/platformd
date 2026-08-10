FROM python:3.13.7-slim-bookworm
WORKDIR /case
COPY requirements.txt ./requirements.txt
RUN pip install --no-cache-dir --requirement requirements.txt
COPY main.py ./main.py
ENTRYPOINT ["python", "main.py"]

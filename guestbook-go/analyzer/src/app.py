import os, json, requests
from flask import Flask, Response, abort, request
from datetime import datetime
import logging
from logging import StreamHandler

# Define the base logger
logging.getLogger("analyzer").setLevel(logging.DEBUG)
log = logging.getLogger("analyzer")
stream_handler = StreamHandler()
stream_formatter = logging.Formatter('[%(asctime)s] [%(thread)d] [%(module)s : %(lineno)d] [%(levelname)s] %(message)s')
stream_handler.setFormatter(stream_formatter)
log.addHandler(stream_handler)

# Flask config
app = Flask(__name__, static_url_path='')
app.config['PROPAGATE_EXCEPTIONS'] = True

# other global variables
tone_analyzer_ep = None


def analyze_tone(input_text):
    log.info("tone_analyzer_ep is: %s ", tone_analyzer_ep)

    r = requests.post(tone_analyzer_ep, headers={'Content-type': 'text/plain'}, data=input_text)
    log.info("tone_analyzer_ep response code is: %s ", r.status_code)
    if r.status_code != 200: 
        log.error("FAILED analyze tone: '%s', msg: '%s'", input_text, r.text)
        return None
    log.info("return %s ", r.json())
    return r.json()

'''
 This is the analyzer API that accepts POST data as describes below:
 POST http://localhost:5000/tone body=\
 {
     "input_text": "this is cool"
 }
'''
@app.route('/tone', methods=['POST'])
def tone():
    if not request.json or not 'input_text' in request.json:
        log.error("bad body: '%s'", request.data)
        abort(400)

    input_text = request.json['input_text']

    log.info("input text is '%s'", input_text)
    tone_doc = analyze_tone(input_text)

    return (json.dumps(tone_doc['document_tone']['tones']), 200)


if __name__ == '__main__':
    PORT = os.getenv('VCAP_APP_PORT', '5000')
    vcap_services = os.getenv('VCAP_SERVICES')
    #id = os.getenv('VCAP_SERVICES_TONE_ANALYZER_0_CREDENTIALS_USERNAME')
    #pwd = os.getenv('VCAP_SERVICES_TONE_ANALYZER_0_CREDENTIALS_PASSWORD')
    #url = os.getenv('VCAP_SERVICES_TONE_ANALYZER_0_CREDENTIALS_URL')
    username = "7a7620bc-c378-4760-986a-1451fb9dacc9"
    pwd = "xxxx"
    url = "gateway.watsonplatform.net/tone-analyzer/api"
    # https://gateway.watsonplatform.net/tone-analyzer/api/v3/tone?version=2017-09-21&sentences=false
    tone_analyzer_ep = "http://" + username + ":" + pwd + "@" + url + "/v3/tone?version=2017-09-21&sentences=false"

    log.info("Starting analyzer tone_analyzer_ep: %s ", tone_analyzer_ep)
    app.run(host='0.0.0.0', port=int(PORT))

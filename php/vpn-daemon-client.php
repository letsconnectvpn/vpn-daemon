<?php

/*
 * To test:
 *
 * make
 * ./_bin/vpn-daemon &
 * php php/vpn-daemon-client.php
 */

$commandList = [
    'SET_PORTS 11940 11941',
    'LIST',
    'DISCONNECT foo bar baz',
    'QUIT',
];

$socket = stream_socket_client('tcp://localhost:41194');

foreach ($commandList as $cmd) {
    var_dump(sendCommand($socket, $cmd));
}

function sendCommand($socket, $cmd)
{
    fwrite($socket, sprintf("%s\n", $cmd));

    return handleResponse($socket);
}

function handleResponse($socket)
{
    $statusLine = fgets($socket);
    if (0 !== strpos($statusLine, 'OK: ')) {
        echo $statusLine;
        exit(1);
    }
    $resultLineCount = (int) substr($statusLine, 4);
    $resultData = [];
    for ($i = 0; $i < $resultLineCount; $i++) {
        $resultData[] = trim(fgets($socket));
    }
   
    return $resultData;
}

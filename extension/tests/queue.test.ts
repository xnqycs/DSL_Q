import test from 'node:test';
import assert from 'node:assert/strict';
import { downloadQueue } from '../src/queue.ts';

test('events during an awaited check cause a second check, without parallel handling',async()=>{
  let release!: ()=>void;const gate=new Promise<void>(r=>release=r);const calls:number[]=[];
  const queue=downloadQueue(async id=>{calls.push(id);if(calls.length===1)await gate;});
  const first=queue(1);await Promise.resolve();const second=queue(1);queue(1);
  assert.equal(first,second);assert.deepEqual(calls,[1]);
  release();await first;assert.deepEqual(calls,[1,1]);
  await queue(1);assert.deepEqual(calls,[1,1,1]);
});

import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';

const fireworkMixers = [];

export function updateFireworks(delta) {
    for (let i = fireworkMixers.length - 1; i >= 0; i--) {
        fireworkMixers[i].update(delta);
    }
}

export function spawnFirework(scene, position) {
    const loader = new GLTFLoader();

    loader.load('firework.glb', (gltf) => {
        const model = gltf.scene;
        if (position) {
            model.position.copy(position);
        }
        scene.add(model);

        const mixer = new THREE.AnimationMixer(model);
        const animations = gltf.animations;

        if (animations && animations.length > 0) {
            const action = mixer.clipAction(animations[0]);
            action.loop = THREE.LoopOnce;
            action.clampWhenFinished = true;
            action.play();

            mixer.addEventListener('finished', () => {
                scene.remove(model);

                model.traverse((object) => {
                    if (object.geometry) object.geometry.dispose();
                    if (object.material) {
                        if (Array.isArray(object.material)) {
                            object.material.forEach(m => m.dispose());
                        } else {
                            object.material.dispose();
                        }
                    }
                });

                const index = fireworkMixers.indexOf(mixer);
                if (index !== -1) {
                    fireworkMixers.splice(index, 1);
                }
            });

            fireworkMixers.push(mixer);
        }
    }, undefined, (error) => {
        console.error('Error loading firework.glb:', error);
    });
}

package metadata

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AddAnnotation(obj metav1.Object, key, value string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	for k, v := range annotations {
		if k == key {
			if v == value {
				return
			}
		}
	}
	annotations[key] = value
	obj.SetAnnotations(annotations)
}

func RemoveAnnotation(obj metav1.Object, key string) {
	annotations := obj.GetAnnotations()
	for k := range annotations {
		if k == key {
			delete(annotations, key)
			obj.SetAnnotations(annotations)
			return
		}
	}
}
